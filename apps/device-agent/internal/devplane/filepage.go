//go:build linux

package devplane

import (
    "errors"
    "io"
    "os"
    "unicode/utf8"
)

type FilePage struct {
    Path string `json:"path"`
    Offset int64 `json:"offset"`
    Text string `json:"text"`
    Bytes int `json:"bytes"`
    NextOffset int64 `json:"nextOffset"`
    HasMore bool `json:"hasMore"`
}

// ReadPage returns a UTF-8-safe byte slice with a stable next byte offset.
func (w *Workspace) ReadPage(path string, offset int64, maxBytes int) (FilePage,error) {
    if offset < 0 || maxBytes < 1 || maxBytes > MaxReadBytes { return FilePage{}, errors.New("invalid offset or maxBytes") }
    fd,err:=w.openRelative(path,false)
    if err!=nil{return FilePage{},err}
    f:=os.NewFile(uintptr(fd),path)
    defer f.Close()
    st,err:=f.Stat()
    if err!=nil{return FilePage{},err}
    if !st.Mode().IsRegular(){return FilePage{}, errors.New("not a regular file")}
    if offset>st.Size(){return FilePage{},errors.New("offset beyond end of file")}
    result:=FilePage{Path:path,Offset:offset,NextOffset:offset}
    if offset==st.Size(){return result,nil}
    if offset>0{
        head:=[]byte{0}
        if _,err=f.ReadAt(head,offset);err!=nil{return FilePage{},err}
        if head[0]&0xC0==0x80{return FilePage{},errors.New("offset splits a UTF-8 codepoint")}
    }
    b:=make([]byte,maxBytes+4)
    n,err:=f.ReadAt(b,offset)
    if err!=nil && err!=io.EOF{return FilePage{},err}
    b=b[:n]
    end:=maxBytes
    if n<end{end=n}
    for end>0 && !utf8.Valid(b[:end]){end--}
    if end==0{return FilePage{},errors.New("invalid UTF-8 or page too small for codepoint")}
    result.Text=string(b[:end])
    result.Bytes=end
    result.NextOffset=offset+int64(end)
    result.HasMore=result.NextOffset<st.Size()
    return result,nil
}
