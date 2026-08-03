# Arkive — Planning Doc

**A Kubernetes Operator for declarative database backup lifecycle management**

Repo: `github.com/<you>/arkive`
API group: `backup.arkive.io`
Language: Go, built on Kubebuilder / controller-runtime

---

## 1. Why this project, and what "done" actually means

The tutorial version of this project stops at "CronJob gets created, backup gets uploaded to S3." That's maybe 30% of what a real operator does. The other 70% — and the part that actually reads as senior-level in an interview — is:

- What happens when you delete the resource? (finalizers, cleanup)
- What happens when two reconciles race, or the controller restarts mid-operation? (idempotency, level-triggered design)
- Can you actually get your data back? (restore path)
- Does `kubectl get databasebackup` tell you anything useful about health? (status conditions)
- Do you have any proof it works beyond "I ran it once on my laptop"? (envtest)

"Done" for this project = all five of the above are implemented, not just the happy-path create flow.

---

## 2. CRD Design

### 2.1 `DatabaseBackup` (the core resource)

```yaml
apiVersion: backup.arkive.io/v1alpha1
kind: DatabaseBackup
metadata:
  name: postgres-daily-backup
  namespace: production
spec:
  database:
    host: postgres-service
    port: 5432
    name: myapp_db
    credentialsSecret:
      name: postgres-credentials
      usernameKey: username    # default: "username"
      passwordKey: password    # default: "password"
  schedule: "0 2 * * *"
  retention:
    count: 7                   # keep last N backups
    # maxAge: "168h"            # alternative: time-based retention (pick one strategy, document why)
  destination:
    type: s3
    bucket: my-backup-bucket
    region: us-east-1
    prefix: "postgres-daily-backup/"   # avoid collisions between DatabaseBackup objects sharing a bucket
  suspend: false                # allows pausing without deleting the resource
status:
  conditions:
    - type: Ready
      status: "True"
      reason: BackupSucceeded
      message: "Last backup completed at 2026-08-03T02:00:11Z"
      lastTransitionTime: "2026-08-03T02:00:11Z"
    - type: Degraded
      status: "False"
      reason: ""
      lastTransitionTime: "2026-08-03T02:00:11Z"
  observedGeneration: 3
  lastScheduledTime: "2026-08-03T02:00:00Z"
  lastSuccessfulBackupTime: "2026-08-03T02:00:11Z"
  lastBackupObjectKey: "postgres-daily-backup/2026-08-03T020000Z.sql.gz"
  activeCronJobRef:
    name: postgres-daily-backup-cronjob
```

**Design decisions worth documenting in your README (interviewers ask "why" not just "what"):**

- **`Conditions` array, not a single status string.** Real operators (cert-manager, cluster-api) use `metav1.Condition` arrays so multiple orthogonal things can be true at once (e.g., "Ready" but "Degraded" because retention cleanup failed even though the backup itself succeeded). A single `status: Healthy|Failed` enum can't express that.
- **`observedGeneration`.** Lets you tell the difference between "spec changed and we haven't reconciled it yet" and "we reconciled the current spec and here's the result." Skipping this is one of the most common tutorial-operator tells.
- **Credentials via secretKeyRef pattern**, never inline. Matches how every real k8s API handles secrets.
- **`suspend: bool`** — CronJobs have this natively; expose it on your CRD too so users can pause without deleting (deleting triggers your finalizer/cleanup, which is a much bigger action than "pause for maintenance").

### 2.2 `DatabaseRestore` (the piece the original spec skipped entirely)

```yaml
apiVersion: backup.arkive.io/v1alpha1
kind: DatabaseRestore
metadata:
  name: restore-prod-2026-08-01
  namespace: production
spec:
  sourceBackupObjectKey: "postgres-daily-backup/2026-08-01T020000Z.sql.gz"
  # OR: sourceBackupRef + pointInTime, resolved to the nearest backup <= that time
  targetDatabase:
    host: postgres-service-restored
    port: 5432
    name: myapp_db_restored
    credentialsSecret:
      name: postgres-credentials
status:
  phase: Succeeded   # Pending | Running | Succeeded | Failed
  conditions: [...]
  startTime: "..."
  completionTime: "..."
```

This is a one-shot Job, not a CronJob — reconcile logic is simpler (create Job if not exists, watch it to completion, report status) but it's a second controller, which is good: it shows you can build more than one controller in the same operator, and shows you understand the difference between a continuously-reconciled resource and a run-once resource.

**Scope call:** implementing this as a real Job that runs `psql` restore is the "advanced" version. A documented, tested manual restore runbook (with the CRD stubbed but not implemented) is an acceptable fallback if the month gets tight — say so explicitly in the README rather than silently dropping it.

---

## 3. Reconciler Design

### 3.1 The reconcile loop, properly shaped

```go
func (r *DatabaseBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)

    var backup backupv1alpha1.DatabaseBackup
    if err := r.Get(ctx, req.NamespacedName, &backup); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 1. Handle deletion via finalizer BEFORE any create/update logic
    if !backup.ObjectMeta.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, &backup)
    }
    if !controllerutil.ContainsFinalizer(&backup, arkiveFinalizer) {
        controllerutil.AddFinalizer(&backup, arkiveFinalizer)
        if err := r.Update(ctx, &backup); err != nil {
            return ctrl.Result{}, err
        }
    }

    // 2. Validate spec / resolve secret ref early — fail fast with a clear condition
    if err := r.validateSpec(ctx, &backup); err != nil {
        r.setCondition(&backup, "Ready", metav1.ConditionFalse, "InvalidSpec", err.Error())
        _ = r.Status().Update(ctx, &backup)
        return ctrl.Result{}, err   // don't requeue on a spec problem the user needs to fix
    }

    // 3. Reconcile the CronJob (create-or-update, not blind create)
    desired := r.buildCronJob(&backup)
    var existing batchv1.CronJob
    err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: backup.Namespace}, &existing)
    switch {
    case errors.IsNotFound(err):
        if err := ctrl.SetControllerReference(&backup, desired, r.Scheme); err != nil {
            return ctrl.Result{}, err
        }
        if err := r.Create(ctx, desired); err != nil {
            return ctrl.Result{}, err
        }
    case err != nil:
        return ctrl.Result{}, err
    default:
        if !cronJobSpecEqual(existing.Spec, desired.Spec) {
            existing.Spec = desired.Spec
            if err := r.Update(ctx, &existing); err != nil {
                return ctrl.Result{}, err
            }
        }
    }

    // 4. Retention cleanup (list S3 objects under prefix, delete beyond N) — separate, non-fatal
    if err := r.enforceRetention(ctx, &backup); err != nil {
        r.setCondition(&backup, "Degraded", metav1.ConditionTrue, "RetentionCleanupFailed", err.Error())
    } else {
        r.setCondition(&backup, "Degraded", metav1.ConditionFalse, "", "")
    }

    // 5. Update status
    backup.Status.ObservedGeneration = backup.Generation
    r.setCondition(&backup, "Ready", metav1.ConditionTrue, "Reconciled", "")
    if err := r.Status().Update(ctx, &backup); err != nil {
        return ctrl.Result{}, err
    }

    return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}
```

**Why each numbered piece matters (for your README / interview talking points):**

1. **Finalizer check first.** If you check spec validity or build resources before checking `DeletionTimestamp`, you risk recreating resources on an object that's mid-delete — a classic operator bug.
2. **Fail fast, don't requeue, on user error.** Returning an error from Reconcile causes controller-runtime to requeue with backoff — appropriate for transient infra errors (API server hiccup), *wrong* for "user typo'd the secret name." Distinguishing these is a real signal of maturity.
3. **Create-or-update, never blind-create.** The original spec's `IsNotFound → create, else → updateCronJobIfNeeded` was actually fine here — keep that shape, but make sure `updateCronJobIfNeeded` does a real diff (`cronJobSpecEqual`) rather than always overwriting, so you're not fighting the k8s API server on every reconcile if nothing changed.
4. **`SetControllerReference`.** This is the piece the original spec entirely omitted. It sets an owner reference so Kubernetes garbage-collects the CronJob automatically when the DatabaseBackup is deleted — you don't have to manually track and delete it. It also means `kubectl delete databasebackup foo` cascades correctly, and `kubectl get cronjob -o yaml` shows you who owns it.
5. **Non-fatal retention cleanup.** A failed S3 cleanup shouldn't fail the whole reconcile / block the next scheduled backup — surface it as `Degraded` instead of `Ready: False`.

### 3.2 Finalizer / deletion handling

```go
func (r *DatabaseBackupReconciler) handleDeletion(ctx context.Context, backup *backupv1alpha1.DatabaseBackup) (ctrl.Result, error) {
    if controllerutil.ContainsFinalizer(backup, arkiveFinalizer) {
        // CronJob cleanup is handled automatically via owner reference (garbage collection) —
        // finalizer is only needed for EXTERNAL resources k8s doesn't know about: S3 objects.
        if backup.Spec.Destination.DeleteOnResourceDeletion {  // explicit opt-in field, don't silently nuke backups
            if err := r.deleteAllBackupObjects(ctx, backup); err != nil {
                return ctrl.Result{}, err   // don't remove finalizer until cleanup succeeds
            }
        }
        controllerutil.RemoveFinalizer(backup, arkiveFinalizer)
        if err := r.Update(ctx, backup); err != nil {
            return ctrl.Result{}, err
        }
    }
    return ctrl.Result{}, nil
}
```

Key point to bake in: **deleting the CRD should NOT silently delete someone's backups by default.** Make it an explicit `spec.destination.deleteOnResourceDeletion: bool` (default `false`). This is a real production-safety consideration, not just plumbing — worth a line in the README explaining the default.

### 3.3 Status conditions helper

Small utility, but shows you're not just stuffing strings into status ad hoc:

```go
func (r *DatabaseBackupReconciler) setCondition(backup *backupv1alpha1.DatabaseBackup, condType string, status metav1.ConditionStatus, reason, message string) {
    meta.SetStatusCondition(&backup.Status.Conditions, metav1.Condition{
        Type:               condType,
        Status:             status,
        Reason:             reason,
        Message:            message,
        ObservedGeneration: backup.Generation,
    })
}
```

(`meta.SetStatusCondition` from `k8s.io/apimachinery/pkg/api/meta` — handles the "only update LastTransitionTime if status actually changed" logic for you, don't reimplement it.)

---

## 4. Testing Strategy (envtest)

Almost nobody building a tutorial-operator does this. Doing it properly is the single highest-leverage differentiator on this whole project.

**Setup:** Kubebuilder scaffolds `envtest` config by default (`make test` spins up a real API server + etcd binary, no real cluster). Your job is to actually write meaningful tests against it, not just leave the scaffolded boilerplate.

**Test cases worth having (this list itself is a good README/interview artifact):**

1. Creating a `DatabaseBackup` results in a `CronJob` being created with the correct schedule and owner reference.
2. Updating `spec.schedule` updates the existing CronJob's schedule (not a delete+recreate).
3. Deleting a `DatabaseBackup` with `deleteOnResourceDeletion: false` leaves S3 objects alone, removes finalizer, and the CronJob disappears via garbage collection.
4. Deleting a `DatabaseBackup` with `deleteOnResourceDeletion: true` calls the (mocked) S3 delete path before the finalizer is removed.
5. An invalid spec (e.g., missing secret) sets `Ready: False` with reason `InvalidSpec` and does NOT create a CronJob.
6. Two reconciles in a row with no spec change produce zero additional API calls to update the CronJob (tests your diffing logic isn't just always-overwrite).

For S3 interactions specifically, mock the S3 client interface (don't hit real AWS in envtest) — inject an interface, provide a fake implementation in tests. This is also a nice, small "I understand dependency injection / testability in Go" signal.

---

## 5. Repo Layout

```
arkive/
├── api/
│   └── v1alpha1/
│       ├── databasebackup_types.go
│       ├── databaserestore_types.go
│       └── groupversion_info.go
├── controllers/
│   ├── databasebackup_controller.go
│   ├── databasebackup_controller_test.go   # envtest suite
│   ├── databaserestore_controller.go
│   └── databaserestore_controller_test.go
├── internal/
│   └── storage/
│       ├── s3.go            # real S3Client interface + implementation
│       └── s3_fake.go       # test double
├── config/
│   ├── crd/                 # generated CRD YAML
│   ├── rbac/                # generated ClusterRole/RoleBinding
│   ├── manager/
│   └── samples/
│       ├── backup_v1alpha1_databasebackup.yaml
│       └── backup_v1alpha1_databaserestore.yaml
├── docs/
│   ├── design-decisions.md   # the "why" writeups from this planning doc, trimmed for public README
│   └── restore-runbook.md    # manual fallback if DatabaseRestore controller is descoped
├── Dockerfile
├── Makefile                  # from Kubebuilder scaffold
├── PROJECT
├── go.mod / go.sum
└── README.md
```

---

## 6. Suggested Build Order (roughly 4–6 weekends)

1. **Weekend 1:** `operator-sdk init` / `kubebuilder init`, scaffold `DatabaseBackup` API + controller. Get the naive create-CronJob path working end to end against a local `kind` cluster.
2. **Weekend 2:** Owner references, status conditions, `observedGeneration`, fail-fast on invalid spec. Get `kubectl get databasebackup` showing real, useful status.
3. **Weekend 3:** Finalizer + S3 cleanup (mocked S3 client), retention enforcement logic.
4. **Weekend 4:** envtest suite — the 6 test cases above at minimum.
5. **Weekend 5 (stretch):** `DatabaseRestore` CRD + controller (Job-based).
6. **Weekend 6 (stretch/buffer):** README with design-decisions doc, sample manifests, demo GIF/recording for the repo.

If time runs short, cut from the bottom of this list, not the top — finalizers/conditions/tests matter more to how this reads than a working restore path.

---

## 7. What's next

I'll turn this into an actual repo scaffold (Kubebuilder-generated skeleton + the pieces above stubbed in) and an agent prompt you can hand off, matching how we did ForgeOps. Let me know if you want to adjust anything above first — particularly the retention strategy (count vs age) or whether you want `DatabaseRestore` in scope from the start vs. stretch goal.
