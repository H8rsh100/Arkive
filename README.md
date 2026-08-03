# 📦 Arkive

> **Declarative Kubernetes Operator for Database Backup & Restore Lifecycle Management**

Arkive is a production-ready Kubernetes operator built with **Go** and **controller-runtime**. It extends the Kubernetes API with custom resources (`DatabaseBackup` and `DatabaseRestore`) to automate database dumps, S3 snapshot shipping, automated retention pruning, and point-in-time restores with level-triggered reconciliation and garbage collection.

---

## 🌟 Key Features & Senior Architecture

Unlike basic tutorial operators that only create CronJobs, **Arkive** implements real-world operator patterns:

1. **Level-Triggered & Idempotent Reconciliation**:
   - Reconciles desired state against observed Kubernetes CronJobs using deep spec comparison (`cronJobSpecEqual`).
   - Prevents unnecessary API server mutations on unchanged reconcile loops.

2. **Kubernetes Native Garbage Collection**:
   - Establishes owner references (`controllerutil.SetControllerReference`) so backing CronJobs and Jobs are automatically garbage collected by Kubernetes when CRDs are deleted (`kubectl delete databasebackup foo`).

3. **Production-Grade Finalizers & S3 Safety**:
   - Utilizes `backup.arkive.io/finalizer` for managing external resources outside Kubernetes control.
   - S3 backup artifacts are **never** deleted by default upon CRD removal unless explicitly requested via `spec.destination.deleteOnResourceDeletion: true`.

4. **Multi-Condition Status Reporting (`metav1.Condition`)**:
   - Exposes standardized Kubernetes condition arrays (`Ready`, `Degraded`).
   - Distinguishes between fatal reconcile errors (e.g., `InvalidSpec`) and non-fatal background operations (e.g., retention cleanup failure causing `Degraded: True` while remaining `Ready: True`).

5. **Retention Policy Pruning**:
   - Automatically inspects S3 bucket prefixes and prunes old snapshot files exceeding the `spec.retention.count` limit.

6. **Dual Controller Architecture**:
   - `DatabaseBackupReconciler`: Continuous cron-based backup snapshot lifecycle.
   - `DatabaseRestoreReconciler`: One-shot Job-based database restoration workflow.

---

## 📐 Architecture & CRD Design

```
+-------------------------------------------------------------------+
|                        Kubernetes Cluster                         |
|                                                                   |
|   +-----------------------+         +-------------------------+   |
|   |  DatabaseBackup CRD   |         |   DatabaseRestore CRD   |   |
|   +-----------+-----------+         +------------+------------+   |
|               |                                  |                |
|               v                                  v                |
|  +-------------------------+        +-------------------------+  |
|  | DatabaseBackupController|        | DatabaseRestoreController|  |
|  +------------+------------+        +------------+------------+  |
|               |                                  |                |
|               v (OwnerRef)                       v (OwnerRef)     |
|        +--------------+                   +-------------+         |
|        |   CronJob    |                   |  RestoreJob |         |
|        +------+-------+                   +------+------+         |
+---------------+----------------------------------+----------------+
                |                                  |
                v (pg_dump / S3)                   v (psql / S3)
       +--------------------------------------------------+
       |                  AWS S3 Storage                  |
       |  s3://bucket/prefix/2026-08-03T020000Z.sql.gz    |
       +--------------------------------------------------+
```

---

## 📄 Custom Resource Definitions

### 1. `DatabaseBackup` Manifest

```yaml
apiVersion: backup.arkive.io/v1alpha1
kind: DatabaseBackup
metadata:
  name: postgres-daily-backup
  namespace: default
spec:
  database:
    host: postgres-service
    port: 5432
    name: myapp_db
    credentialsSecret:
      name: postgres-credentials
      usernameKey: username
      passwordKey: password
  schedule: "0 2 * * *"
  retention:
    count: 7
  destination:
    type: s3
    bucket: my-backup-bucket
    region: us-east-1
    prefix: "postgres-daily-backup/"
    deleteOnResourceDeletion: false
  suspend: false
```

### 2. `DatabaseRestore` Manifest

```yaml
apiVersion: backup.arkive.io/v1alpha1
kind: DatabaseRestore
metadata:
  name: restore-prod-2026-08-01
  namespace: default
spec:
  sourceBackupObjectKey: "postgres-daily-backup/2026-08-01T020000Z.sql.gz"
  targetDatabase:
    host: postgres-service-restored
    port: 5432
    name: myapp_db_restored
    credentialsSecret:
      name: postgres-credentials
      usernameKey: username
      passwordKey: password
```

---

## 🛠️ Repository Layout

```
arkive/
├── api/
│   └── v1alpha1/
│       ├── databasebackup_types.go
│       ├── databaserestore_types.go
│       └── groupversion_info.go
├── controllers/
│   ├── databasebackup_controller.go
│   └── databaserestore_controller.go
├── internal/
│   └── storage/
│       ├── s3.go              # S3 AWS SDK v2 client
│       ├── s3_fake.go         # In-memory test fake
│       └── storage_test.go    # Storage unit test suite
├── config/
│   ├── crd/                   # Generated CustomResourceDefinitions
│   └── samples/               # Example CRD manifests
├── main.go                    # Operator entrypoint
├── go.mod
└── README.md
```

---

## 🚀 Running locally with Kind

1. **Install CRDs onto your Kubernetes cluster**:
   ```bash
   kubectl apply -f config/crd/databasebackups.yaml
   kubectl apply -f config/crd/databaserestores.yaml
   ```

2. **Deploy sample database secret**:
   ```bash
   kubectl create secret generic postgres-credentials \
     --from-literal=username=postgres \
     --from-literal=password=secretpassword
   ```

3. **Deploy DatabaseBackup custom resource**:
   ```bash
   kubectl apply -f config/samples/backup_v1alpha1_databasebackup.yaml
   ```

4. **Verify status**:
   ```bash
   kubectl get databasebackup
   kubectl get cronjob
   ```

---

## 🧪 Testing

Run unit tests for storage layer and controller logic:

```bash
go test -v ./...
```

---

## 📜 License

Apache 2.0 License.
