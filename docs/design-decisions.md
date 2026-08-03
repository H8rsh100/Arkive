# Arkive — Technical Design Decisions

This document summarizes the architectural design choices made in **Arkive**, detailing the engineering rationale behind each decision.

---

## 1. Conditions Array vs. Single Status String

**Decision**: Represent resource state using a standard `[]metav1.Condition` slice rather than a single string field like `status: Healthy` or `status: Failed`.

**Rationale**:
Real-world Kubernetes controllers (such as `cert-manager`, `cluster-api`, and `flux`) use condition arrays so multiple orthogonal statuses can be reported simultaneously. For example:
- A `DatabaseBackup` resource can be **`Ready: True`** (the backup CronJob is configured and actively running) while simultaneously being **`Degraded: True`** (the background S3 retention cleanup failed due to a transient network hiccup).
- A single status enum cannot express orthogonal concerns and leads to status flapping.

---

## 2. Level-Triggered Reconciliation & Spec Diffing

**Decision**: Use level-triggered reconciliation with explicit spec diffing (`cronJobSpecEqual`) before issuing `r.Update` calls to the Kubernetes API server.

**Rationale**:
Edge-triggered controllers miss events if they restart during a state transition. Level-triggered reconciliation evaluates the full desired spec against the current cluster state on every loop.
However, blindly invoking `r.Update` on every reconcile loop places unnecessary load on the API server and generates audit noise. We perform structural equality checks on `CronJob.Spec` fields before mutating existing resources.

---

## 3. Kubernetes Garbage Collection via Owner References

**Decision**: Call `controllerutil.SetControllerReference` to establish parent-child owner references between `DatabaseBackup` $\rightarrow$ `CronJob` and `DatabaseRestore` $\rightarrow$ `Job`.

**Rationale**:
When a user deletes a `DatabaseBackup` (`kubectl delete databasebackup foo`), Kubernetes garbage collection automatically cascades deletion to the backing `CronJob`. The operator does not need complex custom deletion loops for Kubernetes-native child resources.

---

## 4. Explicit Opt-In Finalizer for External S3 Resources

**Decision**: Finalizers (`backup.arkive.io/finalizer`) are used strictly for resources **outside** Kubernetes control (S3 storage objects). S3 objects are only deleted upon CRD removal if `spec.destination.deleteOnResourceDeletion: true` is explicitly enabled.

**Rationale**:
Deleting a CRD should never silently destroy historical database backup snapshots by default. Defaulting to `deleteOnResourceDeletion: false` protects against accidental data loss while allowing users to opt into full automated cleanup when tearing down ephemeral staging environments.

---

## 5. `observedGeneration` Tracking

**Decision**: Record `backup.Status.ObservedGeneration = backup.Generation` upon successful reconciliation.

**Rationale**:
`Generation` increments every time `.spec` is modified. By persisting `ObservedGeneration` in status, clients (`kubectl`, UI dashboards, GitOps tools) can immediately tell whether the status conditions correspond to the latest spec or a stale, pre-reconciliation state.
