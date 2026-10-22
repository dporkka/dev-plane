package devices

import (
 "context"
 "crypto/sha256"
 "database/sql"
 "encoding/hex"
 "errors"
 "fmt"
 "strings"
)

// Device operations are durable control-plane records, not remote execution.
// Caller scope MUST originate from authenticated claims; the capability kernel
// must approve the operation before it is reserved or dispatched.
var (
 ErrInvalidOperation = errors.New("invalid device operation")
 ErrOperationConflict = errors.New("operation ID conflicts with an existing reservation")
 ErrOperationUnavailable = errors.New("device operation not found or not authorized")
 ErrNotDispatchable = errors.New("operation already dispatched or no longer authorized")
)

type OperationSpec struct {
 ID string
 Scope Scope
 DeviceID string
 WorkspaceID string
 Tool string
 Arguments []byte
 Epoch int64
}

var permittedDeviceTools = map[string]bool{
 "workspaces_list":true,"files_list":true,"files_read":true,
 "files_search":true,"jobs_start":true,"jobs_status":true,"jobs_cancel":true,
}

func canonicalDeviceOperationID(id string) bool {
 if len(id)!=68 || !strings.HasPrefix(id,"dp1_") || id[4:]!=strings.ToLower(id[4:]) {return false}
 _,err:=hex.DecodeString(id[4:]);return err==nil
}

// InputDigest is over the exact on-wire argument bytes. Gateway grants must
// separately sign the actor, organization, device, workspace, tool and digest.
func (s OperationSpec) InputDigest() (string,error) {
 if !canonicalDeviceOperationID(s.ID) || strings.TrimSpace(s.Scope.OrganizationID)=="" ||
 strings.TrimSpace(s.Scope.UserID)=="" || strings.TrimSpace(s.DeviceID)=="" ||
 strings.TrimSpace(s.WorkspaceID)=="" || !permittedDeviceTools[s.Tool] || s.Epoch<=0 ||
 len(s.Arguments)>65536 {return "",ErrInvalidOperation}
 input:=append([]byte("dev-plane/device-arguments/v1\x00"),s.Arguments...)
 hash:=sha256.Sum256(input)
 return hex.EncodeToString(hash[:]),nil
}

type ReconnectAction string
const (
 DispatchOnce ReconnectAction="dispatch_once"
 ReconcileWithoutReplay ReconnectAction="reconcile_without_replay"
 ReturnRecordedReceipt ReconnectAction="return_recorded_receipt"
)

// Reconnecting never equates a missing response with a failed execution.
func RecoveryAction(state string) (ReconnectAction,error) {
 switch state {
 case "prepared":return DispatchOnce,nil
 case "dispatched","unknown":return ReconcileWithoutReplay,nil
 case "completed","failed":return ReturnRecordedReceipt,nil
 default:return "",ErrInvalidOperation
 }
}

type OperationRecord struct {
 ID string
 State string
 Tool string
 Digest string
 ResultDigest sql.NullString
 Epoch int64
}

func (r Registry) operationDB() (*sql.DB,error) {
 if r.DB==nil {return nil,errors.New("device registry database unavailable")}
 return r.DB,nil
}

func (r Registry) GetOperation(ctx context.Context, scope Scope, deviceID, workspaceID, operationID string) (OperationRecord,error) {
 db,err:=r.operationDB();if err!=nil{return OperationRecord{},err}
 if scope.OrganizationID=="" || scope.UserID=="" || deviceID=="" || workspaceID=="" || operationID=="" {return OperationRecord{},ErrOperationUnavailable}
 var record OperationRecord
 err=db.QueryRowContext(ctx,`SELECT operation_id,state,tool_name,arguments_digest,result_digest,owner_epoch
 FROM device_operations WHERE operation_id=$1 AND organization_id=$2
 AND actor_user_id=$3 AND device_id=$4 AND workspace_id=$5`,
 operationID,scope.OrganizationID,scope.UserID,deviceID,workspaceID).Scan(
 &record.ID,&record.State,&record.Tool,&record.Digest,&record.ResultDigest,&record.Epoch)
 if errors.Is(err,sql.ErrNoRows){return OperationRecord{},ErrOperationUnavailable}
 if err!=nil{return OperationRecord{},fmt.Errorf("read device operation: %w",err)}
 return record,nil
}

// ReserveOperation is atomic with ownership, membership and workspace-org
// checks. A duplicate ID may only reuse the exact original input and scope.
func (r Registry) ReserveOperation(ctx context.Context, spec OperationSpec) (OperationRecord,error) {
 db,err:=r.operationDB();if err!=nil{return OperationRecord{},err}
 digest,err:=spec.InputDigest();if err!=nil{return OperationRecord{},err}
 _,err=db.ExecContext(ctx,`INSERT INTO device_operations
 (operation_id,organization_id,actor_user_id,device_id,workspace_id,tool_name,arguments_digest,owner_epoch,state)
 SELECT $1,$2,$3,$4,$5,$6,$7,$8,'prepared'
 FROM enrolled_devices d
 JOIN users u ON u.id=d.owner_user_id AND u.organization_id=d.organization_id
 JOIN organizations org ON org.id=d.organization_id
 JOIN workspaces w ON w.id=$5
 JOIN repositories repo ON repo.id=w.repository_id
 JOIN projects project ON project.id=repo.project_id
 WHERE d.device_id=$4 AND d.organization_id=$2 AND d.owner_user_id=$3
 AND d.revoked_at IS NULL AND u.deleted_at IS NULL AND org.deleted_at IS NULL
 AND w.deleted_at IS NULL AND repo.deleted_at IS NULL AND project.deleted_at IS NULL
 AND project.organization_id=$2
 ON CONFLICT (operation_id) DO NOTHING`,
 spec.ID,spec.Scope.OrganizationID,spec.Scope.UserID,spec.DeviceID,spec.WorkspaceID,spec.Tool,digest,spec.Epoch)
 if err!=nil{return OperationRecord{},fmt.Errorf("reserve device operation: %w",err)}
 record,err:=r.GetOperation(ctx,spec.Scope,spec.DeviceID,spec.WorkspaceID,spec.ID)
 if err!=nil{return OperationRecord{},err}
 if record.Tool!=spec.Tool || record.Digest!=digest || record.Epoch!=spec.Epoch {
  return OperationRecord{},ErrOperationConflict
 }
 return record,nil
}

// DispatchOperation is the one-time SQL compare-and-swap before network send.
// Once dispatched, there is deliberately no automatic retryable transition.
func (r Registry) DispatchOperation(ctx context.Context, scope Scope, deviceID,workspaceID,operationID string,epoch int64) error {
 db,err:=r.operationDB();if err!=nil{return err}
 if epoch<=0 || scope.OrganizationID=="" || scope.UserID=="" || deviceID=="" || workspaceID=="" || operationID=="" {return ErrInvalidOperation}
 result,err:=db.ExecContext(ctx,`UPDATE device_operations SET state='dispatched',updated_at=CURRENT_TIMESTAMP
 WHERE operation_id=$1 AND organization_id=$2 AND actor_user_id=$3
 AND device_id=$4 AND workspace_id=$5 AND owner_epoch=$6 AND state='prepared'
 AND EXISTS (
 SELECT 1 FROM enrolled_devices d
 JOIN users u ON u.id=d.owner_user_id AND u.organization_id=d.organization_id
 JOIN organizations org ON org.id=d.organization_id
 JOIN workspaces w ON w.id=$5
 JOIN repositories repo ON repo.id=w.repository_id
 JOIN projects project ON project.id=repo.project_id
 WHERE d.device_id=$4 AND d.organization_id=$2 AND d.owner_user_id=$3
 AND d.revoked_at IS NULL AND u.deleted_at IS NULL AND org.deleted_at IS NULL
 AND w.deleted_at IS NULL AND repo.deleted_at IS NULL AND project.deleted_at IS NULL
 AND project.organization_id=$2)`,operationID,scope.OrganizationID,scope.UserID,deviceID,workspaceID,epoch)
 if err!=nil{return err}
 count,err:=result.RowsAffected();if err!=nil{return err}
 if count!=1{return ErrNotDispatchable}
 return nil
}

func (r Registry) MarkOperationUnknown(ctx context.Context, scope Scope, deviceID,workspaceID,operationID string,epoch int64) error {
 db,err:=r.operationDB();if err!=nil{return err}
 result,err:=db.ExecContext(ctx,`UPDATE device_operations SET state='unknown',updated_at=CURRENT_TIMESTAMP
 WHERE operation_id=$1 AND organization_id=$2 AND actor_user_id=$3
 AND device_id=$4 AND workspace_id=$5 AND owner_epoch=$6 AND state='dispatched'`,
 operationID,scope.OrganizationID,scope.UserID,deviceID,workspaceID,epoch)
 if err!=nil{return err}
 count,err:=result.RowsAffected();if err!=nil{return err}
 if count!=1{return ErrOperationUnavailable}
 return nil
}

// A trusted gateway MUST authenticate and cryptographically verify a signed
// device result (operation ID, epoch, input digest, grant, output digest) before
// calling RecordOperationReceipt. This is NOT a public/unauthenticated RPC.
func (r Registry) RecordOperationReceipt(ctx context.Context, scope Scope, deviceID,workspaceID,operationID string,epoch int64,terminal,resultDigest string) error {
 db,err:=r.operationDB();if err!=nil{return err}
 if (terminal!="completed" && terminal!="failed") || epoch<=0 || len(resultDigest)!=64 {return ErrInvalidOperation}
 if _,err:=hex.DecodeString(resultDigest);err!=nil{return ErrInvalidOperation}
 result,err:=db.ExecContext(ctx,`UPDATE device_operations SET state=$7,result_digest=$8,updated_at=CURRENT_TIMESTAMP
 WHERE operation_id=$1 AND organization_id=$2 AND actor_user_id=$3
 AND device_id=$4 AND workspace_id=$5 AND owner_epoch=$6
 AND state IN ('dispatched','unknown') AND result_digest IS NULL`,
 operationID,scope.OrganizationID,scope.UserID,deviceID,workspaceID,epoch,terminal,resultDigest)
 if err!=nil{return err}
 count,err:=result.RowsAffected();if err!=nil{return err}
 if count==1{return nil}
 existing,err:=r.GetOperation(ctx,scope,deviceID,workspaceID,operationID)
 if err!=nil{return err}
 if existing.Epoch==epoch && existing.State==terminal && existing.ResultDigest.Valid && existing.ResultDigest.String==resultDigest {return nil}
 return ErrOperationConflict
}
