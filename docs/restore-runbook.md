# Database Restore Runbook & Disaster Recovery Procedure

This runbook outlines automated and manual procedures for restoring a database from S3 snapshots managed by **Arkive**.

---

## 🛠️ Method A: Automated Restore via `DatabaseRestore` CRD (Recommended)

1. **Locate the desired backup snapshot key** from S3 or `kubectl get databasebackup`:
   ```bash
   kubectl get databasebackup postgres-daily-backup -o jsonpath='{.status.lastBackupObjectKey}'
   # Output: postgres-daily-backup/2026-08-03T020000Z.sql.gz
   ```

2. **Create a `DatabaseRestore` custom resource**:
   ```yaml
   apiVersion: backup.arkive.io/v1alpha1
   kind: DatabaseRestore
   metadata:
     name: restore-prod-2026-08-03
     namespace: default
   spec:
     sourceBackupObjectKey: "postgres-daily-backup/2026-08-03T020000Z.sql.gz"
     targetDatabase:
       host: postgres-service-target
       port: 5432
       name: myapp_db
       credentialsSecret:
         name: postgres-credentials
   ```

3. **Apply the manifest & observe status**:
   ```bash
   kubectl apply -f restore.yaml
   kubectl get databaserestore -w
   ```

4. **Verify completion**:
   ```bash
   kubectl describe databaserestore restore-prod-2026-08-03
   ```

---

## 🔧 Method B: Manual Fallback Restore Procedure

In emergency scenarios where the Kubernetes operator manager is unreachable:

1. **Download backup snapshot from S3**:
   ```bash
   aws s3 cp s3://my-backup-bucket/postgres-daily-backup/2026-08-03T020000Z.sql.gz ./dump.sql.gz
   gunzip dump.sql.gz
   ```

2. **Stream dump into PostgreSQL target database**:
   ```bash
   kubectl exec -i deployment/postgres-target -- psql -U postgres -d myapp_db < dump.sql
   ```

3. **Verify table integrity**:
   ```bash
   kubectl exec -it deployment/postgres-target -- psql -U postgres -d myapp_db -c "\dt"
   ```
