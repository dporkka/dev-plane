package devices
import (
 "errors"
 "strings"
 "testing"
)
func makeOperationSpec() OperationSpec {
 return OperationSpec{ID:"dp1_"+strings.Repeat("a",64),
 Scope:Scope{OrganizationID:"org",UserID:"user"},
 DeviceID:"device",WorkspaceID:"workspace",Tool:"files_read",
 Arguments:[]byte("{\"path\":\"a\"}"),Epoch:1}
}
func TestDeviceOperationArgumentsAndScope(t *testing.T) {
 original:=makeOperationSpec()
 digest,err:=original.InputDigest();if err!=nil{t.Fatal(err)}
 same,err:=original.InputDigest();if err!=nil || digest!=same {t.Fatal("unstable digest")}
 altered:=original;altered.Arguments=[]byte("{\"path\":\"b\"}")
 second,err:=altered.InputDigest();if err!=nil||digest==second{t.Fatal("argument mutation not detected")}
 for _,change:=range []func(*OperationSpec){
  func(s *OperationSpec){s.ID="bad"},
  func(s *OperationSpec){s.ID="dp1_"+strings.Repeat("A",64)},
  func(s *OperationSpec){s.Scope.OrganizationID=""},
  func(s *OperationSpec){s.Scope.UserID=""},
  func(s *OperationSpec){s.Tool="run_command"},
  func(s *OperationSpec){s.Epoch=0},
  func(s *OperationSpec){s.Arguments=make([]byte,65537)},
 }{
  bad:=original;change(&bad)
  if _,err:=bad.InputDigest();!errors.Is(err,ErrInvalidOperation){t.Errorf("accepted invalid request: %v",err)}
 }
}
func TestDeviceOperationReconnectNeverRetriesDispatchedState(t *testing.T) {
 cases:=[]struct{state string;want ReconnectAction}{
  {"prepared",DispatchOnce},
  {"dispatched",ReconcileWithoutReplay},
  {"unknown",ReconcileWithoutReplay},
  {"completed",ReturnRecordedReceipt},
  {"failed",ReturnRecordedReceipt},
 }
 for _,tc:=range cases{
  got,err:=RecoveryAction(tc.state)
  if err!=nil||got!=tc.want{t.Errorf("%s got %s %v",tc.state,got,err)}
 }
 if _,err:=RecoveryAction("running");!errors.Is(err,ErrInvalidOperation){t.Fatal("unknown state accepted")}
}
