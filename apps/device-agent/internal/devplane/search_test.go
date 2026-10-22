//go:build linux

package devplane

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
)

func TestSearchPaginationAndScope(t *testing.T){
    dir:=t.TempDir()
    for _,name:=range []string{"a.go","b.go","c.go"}{
        if err:=os.WriteFile(filepath.Join(dir,name),[]byte("first NEEDLE match\nsecond needle match\n"),0600);err!=nil{t.Fatal(err)}
    }
    s,err:=NewServer(dir);if err!=nil{t.Fatal(err)};defer s.Close()
    a,err:=s.search.Start(context.Background(),"needle",".",2)
    if err!=nil || a.Outcome!="complete" || len(a.Matches)!=2 || a.NextCursor==""{t.Fatalf("a=%+v err=%v",a,err)}
    b,err:=s.search.Continue(a.NextCursor,2)
    if err!=nil || len(b.Matches)!=2 || b.NextCursor==""{t.Fatalf("b=%+v err=%v",b,err)}
    c,err:=s.search.Continue(b.NextCursor,2)
    if err!=nil || len(c.Matches)!=2 || c.NextCursor!=""{t.Fatalf("c=%+v err=%v",c,err)}
    if a.Matches[0].Path!="a.go" || c.Matches[1].Path!="c.go"{t.Fatal("unstable search order")}
    if _,err:=s.search.Continue(a.NextCursor+"tamper",2);err==nil{t.Fatal("tampered cursor accepted")}
}
func TestSearchHidesSecretsAndRejectsSymlinks(t *testing.T){
    dir,other:=t.TempDir(),t.TempDir()
    for name,data:=range map[string]string{"ok.txt":"needle visible\n",".env":"needle secret\n","credentials.pem":"needle key\n"}{
        if err:=os.WriteFile(filepath.Join(dir,name),[]byte(data),0600);err!=nil{t.Fatal(err)}
    }
    if err:=os.Mkdir(filepath.Join(dir,".git"),0700);err!=nil{t.Fatal(err)}
    os.WriteFile(filepath.Join(dir,".git","config"),[]byte("needle secret"),0600)
    os.WriteFile(filepath.Join(other,"outside"),[]byte("needle outside"),0600)
    os.Symlink(other,filepath.Join(dir,"outlink"))
    os.Symlink(filepath.Join(other,"outside"),filepath.Join(dir,"filelink"))
    s,err:=NewServer(dir);if err!=nil{t.Fatal(err)};defer s.Close()
    p,err:=s.search.Start(context.Background(),"needle",".",30)
    if err!=nil || len(p.Matches)!=1 || p.Matches[0].Path!="ok.txt"{t.Fatalf("leaked hidden content: %+v %v",p,err)}
    if _,err:=s.search.Start(context.Background(),"needle","outlink",20);err==nil{t.Fatal("symlink accepted")}
    if _,err:=s.search.Start(context.Background(),"needle","../",20);err==nil{t.Fatal("traversal accepted")}
}
func TestSearchLimitsAndCursorExpiry(t *testing.T){
    dir:=t.TempDir()
    os.WriteFile(filepath.Join(dir,"a.txt"),[]byte(strings.Repeat("needle\n",500)),0600)
    s,err:=NewServer(dir);if err!=nil{t.Fatal(err)};defer s.Close()
    for _,query:=range []string{"","\n",strings.Repeat("a",201)}{
        if _,err:=s.search.Start(context.Background(),query,".",2);err==nil{t.Fatalf("accepted %q",query)}
    }
    if _,err:=s.search.Start(context.Background(),"needle",".",999);err==nil{t.Fatal("unbounded page")}
    p,err:=s.search.Start(context.Background(),"needle",".",10)
    if err!=nil || p.Outcome!="partial" || p.Reason!="result_limit" || !p.Truncated || p.NextCursor==""{
        t.Fatalf("missing partial outcome: %+v %v",p,err)
    }
    s.search.mu.Lock()
    for _,snap:=range s.search.sessions{snap.expires=time.Now().Add(-time.Second)}
    s.search.mu.Unlock()
    if _,err:=s.search.Continue(p.NextCursor,10);err==nil{t.Fatal("expired cursor accepted")}
}
func TestReadPageUTF8Boundaries(t *testing.T){
    dir:=t.TempDir()
    os.WriteFile(filepath.Join(dir,"unicode.txt"),[]byte("ééé\nend"),0600)
    s,err:=NewServer(dir);if err!=nil{t.Fatal(err)};defer s.Close()
    a,err:=s.workspace.ReadPage("unicode.txt",0,3)
    if err!=nil || a.Text!="é" || a.NextOffset!=2 || !a.HasMore{t.Fatalf("a=%+v err=%v",a,err)}
    b,err:=s.workspace.ReadPage("unicode.txt",a.NextOffset,4)
    if err!=nil || b.Text!="éé" || b.NextOffset!=6{t.Fatalf("b=%+v err=%v",b,err)}
    c,err:=s.workspace.ReadPage("unicode.txt",b.NextOffset,10)
    if err!=nil || c.Text!="\nend" || c.HasMore{t.Fatalf("c=%+v err=%v",c,err)}
    if _,err:=s.workspace.ReadPage("unicode.txt",1,3);err==nil{t.Fatal("misaligned offset accepted")}
}
