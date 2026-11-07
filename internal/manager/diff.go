package manager
import "strings"
const maxConfigDiffLines=120
type ConfigDiff struct {
 InstanceID string `json:"instanceId"`
 InstanceName string `json:"instanceName"`
 Revision uint64 `json:"revision"`
 Changed bool `json:"changed"`
 Added []string `json:"added"`
 Removed []string `json:"removed"`
 AddedCount int `json:"addedCount"`
 RemovedCount int `json:"removedCount"`
 SecretChanged bool `json:"secretChanged"`
 CoreChanged bool `json:"coreChanged"`
 Truncated bool `json:"truncated"`
}
func diffLines(before,after string)(removed,added []string,removedCount,addedCount int,truncated bool){
 oldLines:=strings.Split(strings.TrimSuffix(before,"\n"),"\n"); newLines:=strings.Split(strings.TrimSuffix(after,"\n"),"\n")
 if before=="" {oldLines=nil}; if after=="" {newLines=nil}
 counts:=map[string]int{}; for _,v:=range newLines {counts[v]++}
 for _,v:=range oldLines {if counts[v]>0 {counts[v]--;continue};removedCount++;if len(removed)<maxConfigDiffLines {removed=append(removed,v)}}
 counts=map[string]int{};for _,v:=range oldLines {counts[v]++}
 for _,v:=range newLines {if counts[v]>0 {counts[v]--;continue};addedCount++;if len(added)<maxConfigDiffLines {added=append(added,v)}}
 truncated=removedCount>len(removed)||addedCount>len(added);return
}
func compareConfig(id,name string,revision uint64,applied,draft string,secretChanged,coreChanged bool)ConfigDiff{
 r,a,rc,ac,tr:=diffLines(applied,draft)
 return ConfigDiff{InstanceID:id,InstanceName:name,Revision:revision,Changed:rc>0||ac>0||secretChanged||coreChanged,Added:a,Removed:r,AddedCount:ac,RemovedCount:rc,SecretChanged:secretChanged,CoreChanged:coreChanged,Truncated:tr}
}