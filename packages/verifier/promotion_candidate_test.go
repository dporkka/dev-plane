package verifier

import (
	"context"
	"errors"
	"strings"
	"testing"

	protocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

type candidateRuntime struct {
	head string
	tree string
	parents string
	dirty string
	profiles []string
	failProfile string
}
func (r *candidateRuntime) ExecuteCommand(_ context.Context,_ string,cmd runtimes.Command)(*runtimes.CommandResult,error){
	if len(cmd.Args)>0 {
		switch strings.Join(cmd.Args," ") {
		case "git rev-parse HEAD": return &runtimes.CommandResult{Stdout:r.head+"\n"},nil
		case "git rev-parse HEAD^{tree}":return &runtimes.CommandResult{Stdout:r.tree+"\n"},nil
		case "git show -s --format=%P HEAD":return &runtimes.CommandResult{Stdout:r.parents+"\n"},nil
		case "git status --porcelain --untracked-files=all":return &runtimes.CommandResult{Stdout:r.dirty},nil
		}
		return nil,errors.New("unexpected git command")
	}
	r.profiles=append(r.profiles,cmd.Command)
	if cmd.Command==r.failProfile{return &runtimes.CommandResult{ExitCode:1,Stderr:"failed"},nil}
	return &runtimes.CommandResult{ExitCode:0,Stdout:"ok"},nil
}
type candidateAttemptStore struct {attempts []protocol.VerificationAttempt}
func (s *candidateAttemptStore) InsertVerificationAttempt(_ context.Context,a protocol.VerificationAttempt) error{
	s.attempts=append(s.attempts,a);return nil
}
func candidateRequest() TrustedCandidateRequest {
	return TrustedCandidateRequest{
		SessionID:"isolated-session",WorkItemID:"task-1",Repository:"acme/widget",TargetBranch:"main",
		SourceHeadSHA:strings.Repeat("a",40),TargetBaseSHA:strings.Repeat("b",40),
		CandidateCommitSHA:strings.Repeat("c",40),CandidateTreeSHA:strings.Repeat("d",40),
		PolicyRevisionSHA:strings.Repeat("b",40),
		PolicyBytes:[]byte("version: 1\nverification:\n  unit:\n    command: make unit\n  integration:\n    command: make integration\nwork:\n  isolation: container\n  max_parallel_cost: 1\n"),
		RunnerDigest:"sha256:"+strings.Repeat("e",64),
		SigningKey:[]byte(strings.Repeat("s",32)),
	}
}
func candidateRun(req TrustedCandidateRequest) *candidateRuntime {
	return &candidateRuntime{head:req.CandidateCommitSHA,tree:req.CandidateTreeSHA,
		parents:req.TargetBaseSHA+" "+req.SourceHeadSHA}
}
func TestTrustedCandidateVerifierPersistsSignedExactMergeCandidate(t *testing.T) {
	req:=candidateRequest()
	rt:=candidateRun(req);store:=&candidateAttemptStore{}
	a,err:=VerifyTrustedCandidate(context.Background(),rt,store,req)
	if err!=nil{t.Fatal(err)}
	if len(store.attempts)!=1||store.attempts[0].ID!=a.ID{t.Fatalf("signed result not persisted: %+v",store.attempts)}
	if err:=protocol.VerifyVerificationAttemptSignature(a,req.SigningKey);err!=nil{t.Fatal(err)}
	if err:=a.Passed([]string{"integration","unit"});err!=nil{t.Fatal(err)}
	if len(rt.profiles)!=2||rt.profiles[0]!="make integration"||rt.profiles[1]!="make unit"{
		t.Fatalf("policy profiles not deterministically run: %v",rt.profiles)
	}
}
func TestTrustedCandidateVerifierRejectsDirtyOrWrongTreeBeforeGate(t *testing.T){
	for _,test:=range []struct{name string;dirty string;tree string}{
		{"dirty","?? unexpected.go\n",""},{"wrong tree","",strings.Repeat("9",40)},
	}{
		t.Run(test.name,func(t *testing.T){
			req:=candidateRequest();rt:=candidateRun(req);rt.dirty=test.dirty
			if test.tree!=""{rt.tree=test.tree}
			store:=&candidateAttemptStore{}
			_,err:=VerifyTrustedCandidate(context.Background(),rt,store,req)
			if err==nil||len(rt.profiles)!=0||len(store.attempts)!=0{
				t.Fatalf("verification should fail before gates: %v %+v",err,store)
			}
		})
	}
}
func TestTrustedCandidateVerifierRequiresProtectedPolicyRevision(t *testing.T) {
	req:=candidateRequest();req.PolicyRevisionSHA=req.SourceHeadSHA
	rt:=candidateRun(req);store:=&candidateAttemptStore{}
	_,err:=VerifyTrustedCandidate(context.Background(),rt,store,req)
	if err==nil||len(rt.profiles)!=0||len(store.attempts)!=0{t.Fatal("untrusted policy revision accepted")}
}
func TestTrustedCandidateVerifierPersistsSignedFailureThatCannotPromote(t *testing.T) {
	req:=candidateRequest();rt:=candidateRun(req);rt.failProfile="make integration"
	store:=&candidateAttemptStore{}
	a,err:=VerifyTrustedCandidate(context.Background(),rt,store,req)
	if err==nil {t.Fatal("expected gate failure")}
	if len(store.attempts)!=1{t.Fatalf("failed evidence not stored: %v",store.attempts)}
	if err:=protocol.VerifyVerificationAttemptSignature(a,req.SigningKey);err!=nil{t.Fatal(err)}
	if err:=a.Passed([]string{"unit","integration"});err==nil{t.Fatal("signed failure promoted")}
	if len(rt.profiles)!=1{t.Fatalf("continued past failed gate %v",rt.profiles)}
}
