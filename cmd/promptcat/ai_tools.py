#!/usr/bin/env python3
import ast, difflib, hashlib, json, re, sys
from pathlib import Path, PurePosixPath

PACK = Path(__file__).resolve().parent
ROOT = PACK.parent
PROJECT = PACK / "project.json"
BT = chr(96)
SRC = None
SYM = {}
IMP = {}

EXT = {
    ".go":"go", ".js":"javascript", ".mjs":"javascript", ".cjs":"javascript", ".jsx":"javascript",
    ".ts":"typescript", ".tsx":"typescript", ".vue":"vue", ".svelte":"svelte", ".py":"python", ".rs":"rust",
    ".java":"java", ".kt":"kotlin", ".kts":"kotlin", ".cs":"csharp", ".c":"c", ".h":"c", ".cc":"cpp",
    ".cpp":"cpp", ".hpp":"cpp", ".php":"php", ".rb":"ruby", ".swift":"swift", ".dart":"dart",
    ".ex":"elixir", ".exs":"elixir", ".lua":"lua", ".scala":"scala", ".zig":"zig", ".fs":"fsharp",
    ".fsx":"fsharp", ".clj":"clojure", ".cljs":"clojure", ".cljc":"clojure", ".pl":"perl", ".pm":"perl",
    ".r":"r", ".sh":"shell", ".bash":"shell", ".zsh":"shell", ".json":"json", ".yaml":"yaml",
    ".yml":"yaml", ".toml":"toml", ".xml":"xml", ".md":"markdown",
}
CODE = {"go","javascript","typescript","vue","svelte","python","rust","java","kotlin","csharp","c","cpp","php","ruby","swift","dart","elixir","lua","scala","zig","fsharp","clojure","perl","r","shell"}
CALLABLE = {"function","method","constructor"}
CONTAINER = {"class","struct","interface","trait","module","object","impl","record","protocol"}
REL = CALLABLE | CONTAINER | {"enum","type","constant","macro"}
KEYWORDS = {"if","for","while","switch","match","catch","return","sizeof","typeof","new","delete","func","function","fn","def","class","struct","interface","enum","trait","impl","type","print","println","printf","len","range"}

COMMON_TYPES = [
    ("class", r"^\s*(?:(?:export|public|private|protected|abstract|sealed|open|final)\s+)*class\s+([A-Za-z_]\w*)"),
    ("interface", r"^\s*(?:(?:export|public)\s+)*interface\s+([A-Za-z_]\w*)"),
    ("enum", r"^\s*(?:(?:export|public)\s+)*enum\s+([A-Za-z_]\w*)"),
    ("struct", r"^\s*(?:(?:pub|public)\s+)*struct\s+([A-Za-z_]\w*)"),
]
RULES = {
    "go": [
        ("method", r"^\s*func\s+\([^)]*\)\s+([A-Za-z_]\w*)\s*\("), ("function", r"^\s*func\s+([A-Za-z_]\w*)\s*\("),
        ("struct", r"^\s*type\s+([A-Za-z_]\w*)\s+struct\b"), ("interface", r"^\s*type\s+([A-Za-z_]\w*)\s+interface\b"),
        ("type", r"^\s*type\s+([A-Za-z_]\w*)\b"), ("constant", r"^\s*const\s+([A-Za-z_]\w*)\b"),
    ],
    "python": [("class", r"^\s*class\s+([A-Za-z_]\w*)\b"), ("function", r"^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(")],
    "rust": [
        ("impl", r"^\s*impl(?:<[^>]*>)?\s+(?:[^\s]+\s+for\s+)?([A-Za-z_]\w*)\b"),
        ("function", r"^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)\s*(?:<[^>]*>)?\s*\("),
        ("struct", r"^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)\b"), ("enum", r"^\s*(?:pub\s+)?enum\s+([A-Za-z_]\w*)\b"),
        ("trait", r"^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)\b"), ("type", r"^\s*(?:pub\s+)?type\s+([A-Za-z_]\w*)\b"),
        ("constant", r"^\s*(?:pub\s+)?const\s+([A-Za-z_]\w*)\b"), ("module", r"^\s*(?:pub\s+)?mod\s+([A-Za-z_]\w*)\b"),
    ],
    "ruby": [("class", r"^\s*class\s+([A-Za-z_:]\w*(?:::\w+)*)"), ("module", r"^\s*module\s+([A-Za-z_:]\w*(?:::\w+)*)"), ("function", r"^\s*def\s+(?:self\.)?([A-Za-z_]\w*[!?=]?)")],
    "elixir": [("module", r"^\s*defmodule\s+([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*)"), ("function", r"^\s*defp?\s+([A-Za-z_]\w*[!?]?)")],
    "lua": [("function", r"^\s*(?:local\s+)?function\s+([A-Za-z_]\w*(?:[.:][A-Za-z_]\w*)*)\s*\(")],
    "scala": [("class", r"^\s*(?:case\s+)?class\s+([A-Za-z_]\w*)"), ("trait", r"^\s*trait\s+([A-Za-z_]\w*)"), ("object", r"^\s*object\s+([A-Za-z_]\w*)"), ("function", r"^\s*(?:(?:private|protected|override)\s+)*def\s+([A-Za-z_]\w*)")],
    "zig": [("function", r"^\s*(?:pub\s+)?fn\s+([A-Za-z_]\w*)\s*\("), ("constant", r"^\s*(?:pub\s+)?const\s+([A-Za-z_]\w*)\s*=")],
    "fsharp": [("function", r"^\s*let\s+(?:rec\s+)?([A-Za-z_]\w*)\b"), ("type", r"^\s*type\s+([A-Za-z_]\w*)\b")],
    "clojure": [("function", r"^\s*\(defn-?\s+([A-Za-z_][\w!?*+\-/]*)")],
    "perl": [("function", r"^\s*sub\s+([A-Za-z_]\w*)\b")],
    "r": [("function", r"^\s*([A-Za-z_.][\w.]*)\s*<-\s*function\s*\(")],
    "shell": [("function", r"^\s*(?:function\s+)?([A-Za-z_]\w*)\s*(?:\(\))?\s*\{")],
}
JS_RULES = COMMON_TYPES + [
    ("type", r"^\s*(?:export\s+)?type\s+([A-Za-z_]\w*)\b"),
    ("function", r"^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\("),
    ("function", r"^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>"),
    ("method", r"^\s*(?:(?:public|private|protected|static|async|readonly)\s+)*([A-Za-z_$][\w$]*)\s*\([^;]*\)\s*(?::[^={]+)?\s*\{"),
]
for lang in ("javascript","typescript","vue","svelte"): RULES[lang] = JS_RULES
CFN = COMMON_TYPES + [("function", r"^\s*(?:(?:public|private|protected|static|final|virtual|override|async|inline|extern)\s+)*(?:[A-Za-z_][\w:<>,\[\]?*&.]*\s+)?([A-Za-z_]\w*)\s*\([^;]*\)\s*(?:const\s*)?(?:->[^\{]+)?\s*\{")]
for lang in ("java","csharp","c","cpp","dart"): RULES[lang] = CFN
RULES["kotlin"] = COMMON_TYPES + [("function", r"^\s*(?:(?:public|private|protected|override|suspend)\s+)*fun\s+([A-Za-z_]\w*)\s*\(")]
RULES["swift"] = COMMON_TYPES + [("protocol", r"^\s*protocol\s+([A-Za-z_]\w*)"), ("function", r"^\s*(?:(?:public|private|internal|static|class|override|async)\s+)*func\s+([A-Za-z_]\w*)\s*\(")]
RULES["php"] = COMMON_TYPES + [("trait", r"^\s*trait\s+([A-Za-z_]\w*)"), ("function", r"^\s*(?:(?:public|private|protected|static)\s+)*function\s+([A-Za-z_]\w*)\s*\(")]


def norm(path):
    s = str(path).replace("\\", "/")
    while s.startswith("./"): s = s[2:]
    return s


def project():
    try: return json.loads(PROJECT.read_text(encoding="utf-8"))
    except Exception: return {}


def parse_export():
    p = ROOT / "export.txt"
    if not p.exists(): return {}
    out, current, buf = {}, None, []
    for raw in p.open("r", encoding="utf-8", errors="replace"):
        line = raw.rstrip("\r\n")
        if current is None:
            if line.startswith("<<<FILE: ") and line.endswith(">>>"):
                q = line[len("<<<FILE: "):-3]
                try: current = norm(ast.literal_eval(q))
                except Exception: current = norm(q.strip('"'))
                buf = []
            continue
        if line == "<<<END FILE>>>":
            out[current] = "\n".join(buf); current = None; buf = []; continue
        if line.startswith("\\<<<FILE: ") or line == "\\<<<END FILE>>>": line = line[1:]
        buf.append(line)
    return out


def sources():
    global SRC
    if SRC is not None: return SRC
    SRC = parse_export()
    if SRC: return SRC
    SRC = {}
    for p in ROOT.rglob("*"):
        if not p.is_file(): continue
        rel = norm(p.relative_to(ROOT))
        if rel == "export.txt" or rel.startswith(".promptcat/"): continue
        try: data = p.read_bytes()
        except OSError: continue
        if b"\x00" in data[:8192]: continue
        SRC[rel] = data.decode("utf-8", errors="replace")
    return SRC


def read(path):
    p = norm(path)
    if p not in sources(): raise SystemExit(f"file not found in archive: {path}")
    return sources()[p]


def language(path): return EXT.get(PurePosixPath(path).suffix.lower(), "text")
def is_test(path):
    p = norm(path).lower(); n = PurePosixPath(p).name
    return "/test/" in p or "/tests/" in p or "/__tests__/" in p or n.endswith("_test.go") or ".test." in n or ".spec." in n or n.startswith("test_") or n.endswith("_test.py")
def role(path):
    p = norm(path).lower(); n = PurePosixPath(p).name
    if is_test(path): return "test"
    if p.startswith(".agentpack/") or "/.agentpack/" in p or "/generated/" in p or "/gen/" in p: return "generated"
    if "/migration/" in p or "/migrations/" in p: return "migration"
    if n.endswith(".md") or n.startswith("readme"): return "documentation"
    return "source" if language(path) in CODE else "config"


def code_files(query=None):
    q = query.lower() if query else None
    for p, text in sources().items():
        if language(p) not in CODE or role(p) == "generated": continue
        if q and q not in text.lower() and q not in p.lower(): continue
        yield p, language(p), role(p), text


def mask(lang, text):
    c, out, i, n = list(text), ["\n" if x == "\n" else " " for x in text], 0, len(text)
    slash = lang not in {"python","ruby","shell","r"}; hashc = lang in {"python","ruby","shell","r"}; triple = lang == "python"
    while i < n:
        if slash and i+1<n and c[i]=="/" and c[i+1]=="/":
            i += 2
            while i<n and c[i]!="\n": i += 1
            continue
        if hashc and c[i]=="#":
            i += 1
            while i<n and c[i]!="\n": i += 1
            continue
        if slash and i+1<n and c[i]=="/" and c[i+1]=="*":
            i += 2
            while i+1<n and not (c[i]=="*" and c[i+1]=="/"): i += 1
            i = min(n, i+2); continue
        if triple and i+2<n and c[i] in {"'",'"'} and c[i:i+3]==[c[i]]*3:
            q=c[i]; i+=3
            while i+2<n and c[i:i+3]!=[q]*3: i+=1
            i=min(n,i+3); continue
        if c[i] in {"'",'"',BT}:
            q=c[i]; i+=1
            while i<n:
                if c[i]=="\\" and q!=BT and i+1<n: i+=2; continue
                if c[i]==q: i+=1; break
                i+=1
            continue
        out[i]=c[i]; i+=1
    return "".join(out)


def brace_end(lines, start):
    depth=0; started=False
    for idx in range(start-1, min(len(lines), start+1500)):
        for ch in lines[idx]:
            if ch==";" and not started: return start
            if ch=="{": depth+=1; started=True
            elif ch=="}" and started:
                depth-=1
                if depth==0: return idx+1
        if not started and idx>start+5: break
    return start


def indent_end(lines, start):
    base=len(lines[start-1])-len(lines[start-1].lstrip(" \t")); end=start
    for idx in range(start, len(lines)):
        if not lines[idx].strip(): end=idx+1; continue
        if len(lines[idx])-len(lines[idx].lstrip(" \t")) <= base: break
        end=idx+1
    return end


def sid(path,line,kind,name): return "sym_"+hashlib.sha1(f"{path}:{line}:{kind}:{name}".encode()).hexdigest()[:16]


def symbols(path):
    path=norm(path)
    if path in SYM: return SYM[path]
    text=read(path); lang=language(path); ml=mask(lang,text).splitlines(); rl=text.splitlines(); rows=[]; seen=set()
    for ln,line in enumerate(ml,1):
        for kind,pat in RULES.get(lang,[]):
            m=re.search(pat,line)
            if not m: continue
            name=m.group(1)
            if name in KEYWORDS or (ln,kind,name) in seen: continue
            seen.add((ln,kind,name)); end=ln
            if kind in CALLABLE|CONTAINER|{"enum"}: end=indent_end(ml,ln) if lang in {"python","ruby"} else brace_end(ml,ln)
            rows.append({"id":sid(path,ln,kind,name),"name":name,"kind":kind,"path":path,"language":lang,"line":ln,"endLine":max(ln,end),"signature":rl[ln-1].strip() if ln<=len(rl) else ""}); break
    containers=[r for r in rows if r["kind"] in CONTAINER]
    for r in rows:
        if r["kind"] in CONTAINER: continue
        parents=[p for p in containers if p["line"]<r["line"]<=p["endLine"]]
        if parents:
            p=min(parents,key=lambda x:x["endLine"]-x["line"]); r["parent"]=p["name"]
            if r["kind"]=="function": r["kind"]="method"
    SYM[path]=rows; return rows


def qname(r): return f"{r['parent']}.{r['name']}" if r.get("parent") else r["name"]
def all_symbols(query=None):
    out=[]
    for p,_,_,_ in code_files(query): out.extend(symbols(p))
    return out

def resolve(q,limit=8):
    low=q.lower(); rows=all_symbols(None if low.startswith("sym_") else q)
    exact=[r for r in rows if r["id"]==q or r["name"]==q or qname(r)==q]
    if exact: return exact[:limit]
    exact=[r for r in rows if r["id"].lower()==low or r["name"].lower()==low or qname(r).lower()==low]
    if exact: return exact[:limit]
    if not rows: rows=all_symbols()
    scored=[]
    for r in rows:
        score=max(difflib.SequenceMatcher(None,low,r["name"].lower()).ratio(),difflib.SequenceMatcher(None,low,qname(r).lower()).ratio())
        if low in r["name"].lower() or low in qname(r).lower(): score+=.35
        if low in r["path"].lower(): score+=.1
        if score>=.38:
            x=dict(r); x["score"]=round(min(1,score),3); scored.append(x)
    return sorted(scored,key=lambda x:(-x.get("score",0),x["path"],x["line"]))[:limit]


def resolve_import(src,target):
    fs=sources(); d=PurePosixPath(src).parent; c=[]
    if target.startswith("."):
        b=norm(d/target); c=[b]
        for ext in EXT:
            if ext not in {".json",".yaml",".yml",".toml",".md"}: c += [b+ext,b.rstrip("/")+"/index"+ext]
    elif target.startswith("crate::"):
        b=target[7:].replace("::","/"); c=[b+".rs",b+"/mod.rs"]
    elif "/" in target or "." in target:
        b=target.replace(".","/"); c=[b+e for e in (".py",".go",".java",".kt")]
    return next((norm(x) for x in c if norm(x) in fs),"")


def imports(path):
    path=norm(path)
    if path in IMP: return IMP[path]
    text=read(path); lang=language(path); rows=[]
    def add(t,ln): rows.append({"path":path,"language":lang,"target":t,"line":ln,"resolvedPath":resolve_import(path,t)}) if t else None
    if lang=="go":
        block=False
        for ln,line in enumerate(text.splitlines(),1):
            s=line.strip()
            if s.startswith("import ("): block=True; continue
            if block and s==")": block=False; continue
            m=re.search(r'\bimport\s+(?:[A-Za-z_.]\w*\s+)?"([^"]+)"',s) or (re.search(r'(?:[A-Za-z_.]\w*\s+)?"([^"]+)"',s) if block else None)
            if m: add(m.group(1),ln)
    else:
        pats={
            "python":[r"^\s*from\s+([.\w]+)\s+import\b",r"^\s*import\s+([\w.]+)"],
            "javascript":[r"\bfrom\s+['\"]([^'\"]+)['\"]",r"\bimport\s+['\"]([^'\"]+)['\"]",r"\brequire\s*\(\s*['\"]([^'\"]+)['\"]"],
            "typescript":[r"\bfrom\s+['\"]([^'\"]+)['\"]",r"\bimport\s+['\"]([^'\"]+)['\"]",r"\brequire\s*\(\s*['\"]([^'\"]+)['\"]"],
            "vue":[r"\bfrom\s+['\"]([^'\"]+)['\"]",r"\bimport\s+['\"]([^'\"]+)['\"]"], "svelte":[r"\bfrom\s+['\"]([^'\"]+)['\"]",r"\bimport\s+['\"]([^'\"]+)['\"]"],
            "rust":[r"^\s*use\s+([^;]+);",r"^\s*mod\s+([A-Za-z_]\w*)\s*;"], "java":[r"^\s*import\s+([\w.]+)\s*;"], "kotlin":[r"^\s*import\s+([\w.]+)"],
            "csharp":[r"^\s*using\s+([\w.]+)\s*;"], "c":[r"^\s*#\s*include\s*[<\"]([^>\"]+)[>\"]"], "cpp":[r"^\s*#\s*include\s*[<\"]([^>\"]+)[>\"]"],
            "php":[r"^\s*use\s+([^;]+);",r"\b(?:require|include)(?:_once)?\s*\(?\s*['\"]([^'\"]+)['\"]"], "ruby":[r"^\s*require(?:_relative)?\s+['\"]([^'\"]+)['\"]"], "swift":[r"^\s*import\s+([A-Za-z_]\w*)"],
        }.get(lang,[])
        for ln,line in enumerate(text.splitlines(),1):
            for pat in pats:
                m=re.search(pat,line)
                if m: add(m.group(1).strip(),ln); break
    IMP[path]=rows; return rows


def enclosing(path,line):
    x=[r for r in symbols(path) if r["kind"] in CALLABLE and r["line"]<=line<=r["endLine"]]
    return min(x,key=lambda r:r["endLine"]-r["line"]) if x else None


def refs(query,limit=200):
    targets=resolve(query,12); names={r["name"] for r in targets if r["kind"] in REL} or {r["name"] for r in targets}; out=[]
    pats={n:re.compile(rf"\b{re.escape(n)}\b") for n in names}
    for p,lang,r,text in code_files():
        if r not in {"source","test"} or not any(n in text for n in names): continue
        lines=mask(lang,text).splitlines(); decl={(x["line"],x["name"]) for x in symbols(p)}
        for ln,line in enumerate(lines,1):
            for name,pat in pats.items():
                for m in pat.finditer(line):
                    if (ln,name) in decl: continue
                    caller=enclosing(p,ln); kind="call" if re.match(r"\s*\(",line[m.end():]) else "reference"
                    out.append({"name":name,"path":p,"line":ln,"column":m.start()+1,"kind":kind,"caller":qname(caller) if caller else "","callerId":caller["id"] if caller else ""})
                    if len(out)>=limit: return out
    return out

def callers(q,limit=80): return [r for r in refs(q,limit*4) if r["kind"]=="call" and r.get("caller")][:limit]
def tests(q,limit=80): return [r for r in refs(q,limit*6) if is_test(r["path"])][:limit]
def callees(q,limit=80):
    out=[]; pat=re.compile(r"\b([A-Za-z_]\w*)\s*\(")
    for t in [x for x in resolve(q,8) if x["kind"] in CALLABLE]:
        lines=mask(t["language"],read(t["path"])).splitlines()
        for ln in range(t["line"],min(t["endLine"],len(lines))+1):
            for m in pat.finditer(lines[ln-1]):
                n=m.group(1)
                if n in KEYWORDS or (n==t["name"] and ln==t["line"]): continue
                out.append({"caller":qname(t),"callerId":t["id"],"callee":n,"path":t["path"],"line":ln})
                if len(out)>=limit: return out
    return out


def file_rows(q=""):
    q=q.lower(); rows=[{"path":p,"language":language(p),"role":role(p),"lines":len(t.splitlines())} for p,t in sources().items() if not q or q in p.lower()]
    return sorted(rows,key=lambda r:(r["role"]=="generated",r["path"]))
def compact(r): return f"{qname(r)}\t{r['kind']}\t{r['path']}:{r['line']}-{r['endLine']}"
def emit(rows,json_mode=False,fmt=None):
    for r in rows: print(json.dumps(r,ensure_ascii=False) if json_mode else (fmt(r) if fmt else json.dumps(r,ensure_ascii=False,separators=(",",":"))))

def opt(args,name,default=None):
    flag="--"+name
    if flag not in args: return default
    i=args.index(flag)
    if i+1<len(args) and not args[i+1].startswith("--"): return args[i+1]
    return True

def clean_args(args):
    out=[]; skip=False
    for i,a in enumerate(args):
        if skip: skip=False; continue
        if a in {"--json"}: continue
        if a in {"--limit","--source-lines"}:
            skip=True; continue
        out.append(a)
    return out

def limits(args): return bool(opt(args,"json",False)), int(opt(args,"limit",50)), int(opt(args,"source-lines",80))

def changed(): return project().get("changes") or []
def changed_symbols():
    out=[]
    for ch in changed():
        p=norm(ch.get("path",""))
        if p in sources() and language(p) in CODE: out.extend(symbols(p))
    return out


def usage():
    print("""Promptcat lazy navigator
commands: files map symbol outline imports dependents read search refs callers callees tests context impact changed changed-symbols git
common flags: --json --limit N""")

def main():
    if len(sys.argv)<2: usage(); raise SystemExit(2)
    cmd=sys.argv[1]; raw=sys.argv[2:]; jm,limit,source_lines=limits(raw); a=clean_args(raw)
    if cmd=="files": emit(file_rows(a[0] if a else "")[:limit],jm,lambda r:f"{r['path']}\t{r['language']}\t{r['role']}\t{r['lines']}L")
    elif cmd=="symbol" and a: emit(resolve(a[0],limit),jm,compact)
    elif cmd=="outline" and a: emit(symbols(a[0])[:limit],jm,compact)
    elif cmd=="imports" and a: emit(imports(a[0])[:limit],jm,lambda r:f"{r['target']}\t{r['resolvedPath'] or '-'}\t{r['path']}:{r['line']}")
    elif cmd=="dependents" and a:
        target=norm(a[0]); rows=[r for p,_,_,_ in code_files() for r in imports(p) if r["resolvedPath"]==target or r["target"]==a[0]]; emit(rows[:limit],jm,lambda r:f"{r['path']}:{r['line']}\t{r['target']}")
    elif cmd=="read" and a:
        lines=read(a[0]).splitlines(); start=max(1,int(a[1]) if len(a)>1 else 1); end=min(len(lines),int(a[2]) if len(a)>2 else len(lines))
        for i in range(start-1,end): print(f"{i+1:6} | {lines[i]}")
    elif cmd=="search" and a:
        q=a[0].lower(); prefix=norm(a[1]) if len(a)>1 else ""; count=0
        for p,text in sources().items():
            if prefix and not p.startswith(prefix): continue
            for i,line in enumerate(text.splitlines(),1):
                if q in line.lower():
                    print(f"{p}:{i}:{line.strip()}"); count+=1
                    if count>=limit: return
    elif cmd=="refs" and a: emit(refs(a[0],limit),jm,lambda r:f"{r['kind']}\t{r['path']}:{r['line']}:{r['column']}\t{r.get('caller') or '-'}")
    elif cmd=="callers" and a: emit(callers(a[0],limit),jm,lambda r:f"{r['caller']}\t{r['path']}:{r['line']}")
    elif cmd=="callees" and a: emit(callees(a[0],limit),jm,lambda r:f"{r['callee']}\t{r['path']}:{r['line']}")
    elif cmd=="tests" and a: emit(tests(a[0],limit),jm,lambda r:f"{r['path']}:{r['line']}\t{r['kind']}\t{r.get('caller') or '-'}")
    elif cmd=="changed": emit(changed()[:limit],jm,lambda r:f"{r.get('status','')}\t{r.get('path','')}")
    elif cmd=="changed-symbols": emit(changed_symbols()[:limit],jm,compact)
    elif cmd=="git": print(json.dumps(project().get("git") or {},ensure_ascii=False,indent=2 if jm else None))
    elif cmd=="map":
        for r in file_rows(a[0] if a else "")[:limit]:
            print(f"{r['path']} [{r['language']}] [{r['role']}]")
            if r["language"] in CODE and r["role"]!="generated":
                for s in [x for x in symbols(r["path"]) if not x.get("parent")][:6]: print(f"  {s['kind']} {s['name']} @{s['line']}")
    elif cmd=="context" and a:
        targets=resolve(a[0],3)
        if not targets: raise SystemExit(f"symbol not found: {a[0]}")
        for t in targets:
            print("SYMBOL",compact(t)); print("SOURCE"); lines=read(t["path"]).splitlines(); start=max(1,t["line"]-4); end=min(len(lines),t["endLine"]+4,t["line"]+source_lines)
            for i in range(start-1,end): print(f"{i+1:6} | {lines[i]}")
            for title,rows,fmt in [("CALLERS",callers(t["id"],limit),lambda r:f"{r['caller']} {r['path']}:{r['line']}"),("CALLEES",callees(t["id"],limit),lambda r:f"{r['callee']} {r['path']}:{r['line']}"),("TESTS",tests(t["id"],limit),lambda r:f"{r['path']}:{r['line']} {r.get('caller') or ''}"),("IMPORTS",imports(t["path"])[:limit],lambda r:f"{r['target']} -> {r['resolvedPath'] or '-'}")]:
                print(title); print("  (none)" if not rows else "\n".join("  "+fmt(r).rstrip() for r in rows))
            print()
    elif cmd=="impact" and a:
        ts=resolve(a[0],5)
        if not ts: raise SystemExit(f"symbol not found: {a[0]}")
        rr=refs(a[0],limit*4); affected={r["path"] for r in ts+rr}
        for t in ts:
            for p,_,_,_ in code_files():
                if any(x["resolvedPath"]==t["path"] for x in imports(p)): affected.add(p)
        print(f"symbols={len(ts)} refs={len(rr)} callers={sum(r['kind']=='call' for r in rr)} tests={sum(is_test(r['path']) for r in rr)} affected_files={len(affected)}")
        for p in sorted(affected)[:limit]: print(p)
    else: usage(); raise SystemExit(2)

if __name__=="__main__": main()
