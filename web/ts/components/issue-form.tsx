import { useState } from "preact/hooks";
import { api, type Issue, type IssueCreate, type Relation } from "../api.js";
import { issueEditorShortcut } from "../keyboard.js";
import { stagedLabel } from "../issue-create.js";
import { Button, Field, MarkdownInput, Modal, ErrorMessage, Loading, useResource, useMutation, useApp } from "./ui.js";
import { Autocomplete } from "./autocomplete.js";
export function IssueFields({issue}: {issue?: Pick<Issue,"title"|"description"|"commit_hash"|"pull_request_url">}) {
  return <><Field label="Title"><input name="title" value={issue?.title??""} required maxLength={500} autofocus /></Field><div class="edit-field"><span class="edit-field-label">Description (Markdown)</span><MarkdownInput value={issue?.description??""} name="description" label="Issue description (Markdown)"/></div><div class="edit-field-row"><Field label="Commit"><input name="commit_hash" value={issue?.commit_hash??""} pattern="[0-9A-Fa-f]{8,128}" maxLength={128} placeholder="Optional commit hash"/></Field><Field label="Pull request"><input name="pull_request_url" value={issue?.pull_request_url??""} type="url" maxLength={1000} placeholder="https://…"/></Field></div></>;
}
export function issueFields(form: HTMLFormElement) {
  const values=new FormData(form);
  return {title:String(values.get("title")??""), description:String(values.get("description")??""),commit_hash:String(values.get("commit_hash")??""),pull_request_url:String(values.get("pull_request_url")??"")};
}
export function FilePicker({onFiles}: {onFiles:(files:File[])=>void}) {
  const [drag,setDrag]=useState(false);
  return <div class={`compact-editor attachment-editor ${drag?"drag-active":""}`} onDragOver={e=>{e.preventDefault();setDrag(true);}} onDragLeave={()=>setDrag(false)} onDrop={e=>{e.preventDefault();setDrag(false);onFiles(Array.from(e.dataTransfer?.files??[]));}}><label class="attachment-picker">Drop files here or <span class="attachment-browse">browse</span><input type="file" multiple class="attachment-file-input" aria-label="Attachment files" onChange={e=>{onFiles(Array.from(e.currentTarget.files??[]));e.currentTarget.value="";}}/></label></div>;
}
export function RelationFields({onAdd}: {onAdd:(type:Relation["type"],other:string)=>void}) {
  const [open,setOpen]=useState(false);const [type,setType]=useState<Relation["type"]>("blocked-by");const [other,setOther]=useState("");
  return open ? <div class="relation-fields"><select aria-label="Relation type" value={type} onChange={e=>setType(e.currentTarget.value as Relation["type"])}>{["blocked-by","has-parent","discovered-from","related"].map(type=><option key={type}>{type}</option>)}</select><Autocomplete value={other} onValue={setOther} aria-label="Other issue ID" placeholder="Other issue ID" load={async(q,s)=>(await api.issueSuggestions(q,s)).rows.map(i=>({value:i.id,label:i.id,detail:i.title}))}/><Button class="quiet-action" onClick={()=>{onAdd(type,other.trim());setOther("");setOpen(false);}}>Add relation</Button></div>:<div class="relation-disclosure-row"><Button class="relation-disclosure" onClick={()=>setOpen(true)}>+ Add relation</Button><span class="relation-hint">Create a dependency or association</span></div>;
}
export function IssueCreateButton({label="New issue",workspace,epic,assignToMe=false,onCreated,className="primary-button"}: {label?:string;workspace?:string;epic?:Issue;assignToMe?:boolean;onCreated:()=>void|Promise<void>;className?:string}) {
  const [open,setOpen]=useState(false);
  return <><Button class={className} onClick={()=>setOpen(true)}>{label}</Button>{open&&<CreateIssue workspace={workspace} epic={epic} assignToMe={assignToMe} onClose={()=>setOpen(false)} onCreated={onCreated}/>}</>;
}
function CreateIssue({workspace:initial,epic,assignToMe,onClose,onCreated}: {workspace?:string;epic?:Issue;assignToMe:boolean;onClose:()=>void;onCreated:()=>void|Promise<void>}) {
  const {identity,notify}=useApp();const resource=useResource(()=>api.workspaces(),[]);const mutation=useMutation();
  const [workspace,setWorkspace]=useState(initial??epic?.workspace??"");const [label,setLabel]=useState("");const [labels,setLabels]=useState<string[]>([]);
  const [relations,setRelations]=useState<NonNullable<IssueCreate["relations"]>>(epic?[{type:"has-parent",other:epic.id}]:[]);const [files,setFiles]=useState<File[]>([]);const [error,setError]=useState<unknown>();
  const stage=()=>{const result=stagedLabel(label,labels);if(result.error!==undefined){setError(result.error);return null;}const next=[...labels,result.label];setLabels(next);setLabel("");setError(undefined);return next;};
  return <Modal title="New issue" className="issue-create-dialog" onClose={onClose}><ErrorMessage error={resource.error}/>{!resource.data?<Loading/>:resource.data.rows.length===0?<p>Create a workspace before creating an issue.</p>:<form class="issue-create-form" onKeyDown={e=>{if(issueEditorShortcut(e)==="save"){e.preventDefault();e.currentTarget.requestSubmit();}}} onSubmit={e=>{
    e.preventDefault();const form=e.currentTarget;const data=new FormData(form);const staged=label.trim()?stage():labels;if(staged===null)return;
    void mutation.run(async()=>{
      const created=await api.createIssue({...issueFields(form),workspace:workspace||resource.data!.rows[0].key,type:String(data.get("type")) as IssueCreate["type"],priority:Number(data.get("priority")),...(data.get("assign")&&identity?{assignees:[identity]}:{}),labels:staged,relations});
      const uploads=await Promise.allSettled(files.map(file=>api.addAttachment(created.id,file)));
      const failures=uploads.flatMap((result,index)=>result.status==="rejected"?[`${files[index].name}: ${String(result.reason)}`]:[]);
      notify(failures.length?`Issue ${created.id} was created, but attachment uploads failed: ${failures.join(", ")}`:`Issue ${created.id} was created.`,failures.length>0);
      await onCreated();onClose();
    });
  }}>
    {epic&&<p class="issue-create-context">Epic: {epic.id} · {epic.title}</p>}
    <div class="edit-field-row"><Field label="Workspace"><select name="workspace" value={workspace||resource.data.rows[0].key} disabled={!!epic} onChange={e=>setWorkspace(e.currentTarget.value)}>{resource.data.rows.filter(w=>w.state==="active").map(w=><option key={w.key}>{w.key}</option>)}</select></Field><Field label="Type"><select name="type" value="task">{["task","feature","bug","epic","chore"].map(t=><option key={t}>{t}</option>)}</select></Field><Field label="Priority"><select name="priority" value="2">{[0,1,2,3,4].map(p=><option key={p} value={p}>P{p}</option>)}</select></Field></div>
    <IssueFields/>
    <div class="issue-create-resources"><section class="issue-create-resource issue-create-labels"><h3>Labels</h3><div class="issue-create-label-list">{labels.map(item=><span class="editable-chip" key={item}><span class="listing-badge label">{item}</span><Button class="chip-remove" aria-label={`Remove label ${item}`} onClick={()=>setLabels(labels.filter(v=>v!==item))}>×</Button></span>)}</div><div class="compact-editor issue-create-label-editor"><Autocomplete value={label} onValue={setLabel} aria-label="Label" placeholder="Add label" maxLength={64} load={async(q,s)=>(await api.labels({workspace:[workspace||resource.data!.rows[0].key]},s)).rows.filter(v=>!labels.includes(v.value)&&v.value.includes(q)).map(v=>({value:v.value,label:v.value}))}/><Button class="quiet-action" onClick={stage}>Add</Button></div></section>
    <section class="issue-create-resource"><h3>Relations</h3><ul class="relations resource-list issue-create-resource-list">{relations.map((r,index)=><li key={`${r.type}:${r.other}`}><span class="id">New issue</span><span class="relation-type">{r.type}</span><span class="id">{r.other}</span>{r.other!==epic?.id&&<Button class="inline-button danger-button" onClick={()=>setRelations(relations.filter((_,i)=>i!==index))}>Remove</Button>}</li>)}</ul><RelationFields onAdd={(type,other)=>{if(!other){setError("Choose another issue.");return;}if(relations.some(r=>r.type===type&&(r.other===other||type==="has-parent"))){setError("That relation or parent is already staged.");return;}setRelations([...relations,{type,other}]);}}/></section>
    <section class="issue-create-resource"><h3>Attachments</h3><ul class="attachments resource-list">{files.map(file=><li key={file.name}>{file.name}<Button class="inline-button danger-button" onClick={()=>setFiles(files.filter(f=>f.name!==file.name))}>Remove</Button></li>)}</ul><FilePicker onFiles={next=>setFiles([...new Map([...files,...next].map(f=>[f.name,f])).values()])}/></section></div>
    <ErrorMessage error={error??mutation.error}/><div class="edit-actions"><label class="issue-create-assign"><input type="checkbox" name="assign" defaultChecked={assignToMe}/>Assign to me{identity?` (@${identity})`:""}</label><Button onClick={onClose}>Cancel</Button><Button type="submit" class="primary-button" disabled={mutation.busy}>Create issue</Button></div>
  </form>}</Modal>;
}
