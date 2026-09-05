import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../api.js";
import { namedDestinations, workspaceScopedHref } from "../navigation.js";
import { filterCommands, navigationResultCommands, isPaletteShortcut, nextPaletteSelection, visiblePalettePageSize, isPlainPaletteBoundaryKey, paletteShortcutHint, paletteTrigger, type PaletteCommand, type PaletteNavigationKey } from "../command-palette.js";
import { SuggestionSearch, type SuggestionState } from "../autocomplete.js";
import { type Route } from "../routing/route.js";
import { Button, Modal, useApp } from "./ui.js";
export function Palette({route}:{route:Route}) {
  const [open,setOpen]=useState(false);
  useEffect(()=>{const shortcut=(e:KeyboardEvent)=>{if(isPaletteShortcut(e)){e.preventDefault();setOpen(value=>!value);}};window.addEventListener("keydown",shortcut);return()=>window.removeEventListener("keydown",shortcut);},[]);
  return <><Button class="command-palette-button" title={paletteTrigger.title} aria-keyshortcuts={paletteTrigger.keyShortcuts} onClick={()=>setOpen(true)}>{paletteTrigger.label}<kbd>{paletteShortcutHint()}</kbd></Button>{open&&<PaletteDialog route={route} onClose={()=>setOpen(false)}/>}</>;
}
function PaletteDialog({route,onClose}:{route:Route;onClose:()=>void}) {
  const {identity}=useApp();const [query,setQuery]=useState("");const [remote,setRemote]=useState<PaletteCommand[]>([]);const [status,setStatus]=useState<SuggestionState>("idle");const [selected,setSelected]=useState(0);
  const input=useRef<HTMLInputElement>(null);const list=useRef<HTMLDivElement>(null);const search=useRef<SuggestionSearch<PaletteCommand>>();
  const go=(href:string)=>{onClose();location.hash=href;};
  const all:PaletteCommand[]=namedDestinations.map(d=>({id:`view:${d.id}`,label:`Go to ${d.label}`,hint:"View",keywords:d.keywords,group:"Navigation",run:()=>go(d.workspaceScoped?workspaceScopedHref(d.workspaceScoped,route.query):d.path)}));
  if(identity)all.push({id:"view:profile",label:"Go to profile",hint:`@${identity}`,keywords:"account settings password",group:"Navigation",run:()=>go("#/profile")});
  const commands=[...filterCommands(all,query),...remote];
  useEffect(()=>{const next=new SuggestionSearch(async(q,s)=>navigationResultCommands(await api.navigation(q,s),go),(status,rows)=>{setStatus(status);setRemote(rows);setSelected(0);});search.current=next;return()=>next.close();},[]);
  useEffect(()=>{list.current?.querySelector<HTMLElement>(`#palette-option-${selected}`)?.scrollIntoView({block:"nearest"});},[selected]);
  return <Modal title="Commands" className="command-palette" onClose={onClose}><div class="command-palette-search search-control"><input ref={input} autofocus type="text" value={query} placeholder="Search commands, issues, workspaces, users…" aria-label="Search commands" role="combobox" aria-expanded="true" aria-controls="palette-results" aria-activedescendant={commands.length?`palette-option-${selected}`:undefined} onInput={e=>{setQuery(e.currentTarget.value);setSelected(0);search.current?.query(e.currentTarget.value);}} onKeyDown={e=>{
    if(e.key==="Enter"){e.preventDefault();commands[selected]?.run();return;}
    if(["ArrowDown","ArrowUp","PageDown","PageUp"].includes(e.key)||isPlainPaletteBoundaryKey(e)){e.preventDefault();setSelected(nextPaletteSelection(e.key as PaletteNavigationKey,selected,commands.length,list.current?visiblePalettePageSize(list.current,selected,e.key==="PageUp"?-1:1):1));}
  }}/><Button class="search-clear" aria-label="Clear search" hidden={!query} onClick={()=>{setQuery("");search.current?.query("");setSelected(0);input.current?.focus();}}>×</Button></div><div class="command-palette-status" role="status">{status==="loading"?"Searching…":status==="error"?"Search unavailable":commands.length?`${commands.length} results`:"No results"}</div><div ref={list} id="palette-results" class="command-palette-results" role="listbox">{commands.map((command,index)=><div key={command.id}>{(index===0||commands[index-1].group!==command.group)&&<div class="command-palette-group">{command.group}</div>}<div id={`palette-option-${index}`} class={`command-palette-option ${index===selected?"active":""}`} role="option" aria-selected={index===selected} onPointerMove={()=>setSelected(index)} onClick={command.run}><span class="command-palette-label">{command.label}</span><span class="command-palette-hint">{command.hint}</span></div></div>)}</div></Modal>;
}
