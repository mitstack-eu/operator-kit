package main

// The guard pins that manifest content under blockverify/ and handler/ is only handed to contract.ParseManifest, measured with len, compared with nil, closed or hashed with sha256.Sum256.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	kitContractPath = "github.com/mitstack-eu/operator-kit/v2/contract"
	kitHandlerPath  = "github.com/mitstack-eu/operator-kit/v2/handler"
	kitBlockverify  = "github.com/mitstack-eu/operator-kit/v2/blockverify"
)

type guardPackage struct {
	path  string
	files []*ast.File
}

type guardFunc struct {
	key            string
	pkg            *guardPackage
	file           *ast.File
	decl           *ast.FuncDecl
	params         []*ast.Ident
	contentParam   []bool
	pathParam      []bool
	returnsContent bool
	returnsPath    bool
}

type manifestReadGuard struct {
	fset       *token.FileSet
	pkgs       map[string]*guardPackage
	funcs      []*guardFunc
	byKey      map[string][]*guardFunc
	imports    map[*ast.File]map[string]string
	consts     map[string]map[string]ast.Expr
	changed    bool
	record     bool
	sources    map[token.Pos]string
	violations map[token.Pos]string
}

func guardManifestReads(fset *token.FileSet, pkgs []*guardPackage) (map[string]int, []string) {
	g := &manifestReadGuard{
		fset:       fset,
		pkgs:       map[string]*guardPackage{},
		byKey:      map[string][]*guardFunc{},
		imports:    map[*ast.File]map[string]string{},
		consts:     map[string]map[string]ast.Expr{},
		sources:    map[token.Pos]string{},
		violations: map[token.Pos]string{},
	}
	for _, p := range pkgs {
		g.pkgs[p.path] = p
		for _, f := range p.files {
			names := map[string]string{}
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				name := path[strings.LastIndex(path, "/")+1:]
				if imp.Name != nil {
					name = imp.Name.Name
				}
				names[name] = path
			}
			g.imports[f] = names
			g.collectConsts(p.path, f)
			for _, d := range f.Decls {
				decl, ok := d.(*ast.FuncDecl)
				if !ok || decl.Body == nil {
					continue
				}
				g.addFunc(p, f, decl)
			}
		}
	}
	for {
		g.changed = false
		for _, fn := range g.funcs {
			g.analyse(fn)
		}
		if !g.changed {
			break
		}
	}
	g.record = true
	for _, fn := range g.funcs {
		g.analyse(fn)
	}

	counts := map[string]int{}
	for _, pkg := range g.sources {
		counts[pkg]++
	}
	var out []string
	for _, v := range g.violations {
		out = append(out, v)
	}
	sort.Strings(out)
	return counts, out
}

func (g *manifestReadGuard) addFunc(p *guardPackage, f *ast.File, decl *ast.FuncDecl) {
	fn := &guardFunc{pkg: p, file: f, decl: decl}
	key := p.path + "." + decl.Name.Name
	if decl.Recv != nil {
		key = p.path + ".*." + decl.Name.Name
	}
	fn.key = key
	for _, field := range decl.Type.Params.List {
		names := field.Names
		if len(names) == 0 {
			names = []*ast.Ident{nil}
		}
		for _, name := range names {
			seed := name != nil && isByteSlice(field.Type) &&
				(saysManifest(decl.Name.Name) || saysManifest(name.Name))
			fn.params = append(fn.params, name)
			fn.contentParam = append(fn.contentParam, seed)
			fn.pathParam = append(fn.pathParam, false)
			if seed {
				g.sources[name.Pos()] = p.path
			}
		}
	}
	g.funcs = append(g.funcs, fn)
	g.byKey[key] = append(g.byKey[key], fn)
}

func (g *manifestReadGuard) collectConsts(pkgPath string, f *ast.File) {
	if g.consts[pkgPath] == nil {
		g.consts[pkgPath] = map[string]ast.Expr{}
	}
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, name := range vs.Names {
				if i < len(vs.Values) {
					g.consts[pkgPath][name.Name] = vs.Values[i]
				}
			}
		}
	}
}

func (st *funcState) constString(e ast.Expr, depth int) (string, bool) {
	if depth > 16 {
		return "", false
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		v, err := strconv.Unquote(e.Value)
		return v, err == nil
	case *ast.ParenExpr:
		return st.constString(e.X, depth+1)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, okX := st.constString(e.X, depth+1)
		y, okY := st.constString(e.Y, depth+1)
		return x + y, okX && okY
	case *ast.Ident:
		if e.Obj != nil {
			if e.Obj.Kind != ast.Con {
				return "", false
			}
			if vs, ok := e.Obj.Decl.(*ast.ValueSpec); ok {
				for i, name := range vs.Names {
					if name.Name == e.Name && i < len(vs.Values) {
						return st.constString(vs.Values[i], depth+1)
					}
				}
			}
		}
		if v, ok := st.g.consts[st.fn.pkg.path][e.Name]; ok {
			return st.constString(v, depth+1)
		}
	case *ast.SelectorExpr:
		if path, ok := st.pkgOf(e.X); ok {
			if v, ok := st.g.consts[path][e.Sel.Name]; ok {
				return st.constString(v, depth+1)
			}
		}
	}
	return "", false
}

func saysManifest(name string) bool { return strings.Contains(strings.ToLower(name), "manifest") }

func isByteSlice(e ast.Expr) bool {
	a, ok := e.(*ast.ArrayType)
	if !ok || a.Len != nil {
		return false
	}
	id, ok := a.Elt.(*ast.Ident)
	return ok && id.Name == "byte"
}

type funcState struct {
	g       *manifestReadGuard
	fn      *guardFunc
	content map[*ast.Object]bool
	path    map[*ast.Object]bool
}

func (g *manifestReadGuard) analyse(fn *guardFunc) {
	st := &funcState{g: g, fn: fn, content: map[*ast.Object]bool{}, path: map[*ast.Object]bool{}}
	for i, p := range fn.params {
		if p == nil || p.Obj == nil {
			continue
		}
		if fn.contentParam[i] {
			st.content[p.Obj] = true
		}
		if fn.pathParam[i] {
			st.path[p.Obj] = true
		}
	}
	for {
		before := len(st.content) + len(st.path)
		st.walk()
		if len(st.content)+len(st.path) == before {
			return
		}
	}
}

func (st *funcState) walk() {
	var stack []ast.Node
	ast.Inspect(st.fn.decl.Body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		stack = append(stack, n)
		switch n := n.(type) {
		case *ast.AssignStmt:
			st.propagatePath(n.Lhs, n.Rhs)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(n.Names))
			for i, name := range n.Names {
				lhs[i] = name
			}
			st.propagatePath(lhs, n.Values)
		case *ast.ReturnStmt:
			if st.inFuncLit(stack) {
				break
			}
			for _, r := range n.Results {
				if st.isPath(r) && !st.fn.returnsPath {
					st.fn.returnsPath, st.g.changed = true, true
				}
			}
		case *ast.CallExpr:
			for i, arg := range n.Args {
				if !st.isPath(arg) {
					continue
				}
				for _, target := range st.resolve(n) {
					st.g.mark(target.pathParam, i)
				}
			}
		}
		if e, ok := n.(ast.Expr); ok && st.isContent(e) {
			st.checkUse(stack)
		}
		return true
	})
}

func (g *manifestReadGuard) mark(flags []bool, i int) {
	if len(flags) == 0 {
		return
	}
	if i >= len(flags) {
		i = len(flags) - 1
	}
	if !flags[i] {
		flags[i], g.changed = true, true
	}
}

func (st *funcState) propagatePath(lhs, rhs []ast.Expr) {
	for i, r := range rhs {
		if !st.isPath(r) {
			continue
		}
		var targets []ast.Expr
		if len(lhs) == len(rhs) {
			targets = lhs[i : i+1]
		} else {
			targets = lhs
		}
		for _, t := range targets {
			if id, ok := t.(*ast.Ident); ok && id.Obj != nil {
				st.path[id.Obj] = true
			}
		}
	}
}

func (st *funcState) inFuncLit(stack []ast.Node) bool {
	for i := len(stack) - 1; i >= 0; i-- {
		if _, ok := stack[i].(*ast.FuncLit); ok {
			return true
		}
	}
	return false
}

func (st *funcState) pkgOf(x ast.Expr) (string, bool) {
	id, ok := x.(*ast.Ident)
	if !ok || id.Obj != nil {
		return "", false
	}
	path, ok := st.g.imports[st.fn.file][id.Name]
	return path, ok
}

func (st *funcState) isPkgFunc(fun ast.Expr, pkgPath, name string) bool {
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	path, ok := st.pkgOf(sel.X)
	return ok && path == pkgPath
}

func (st *funcState) resolve(call *ast.CallExpr) []*guardFunc {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fun.Obj != nil && fun.Obj.Kind != ast.Fun {
			return nil
		}
		return st.g.byKey[st.fn.pkg.path+"."+fun.Name]
	case *ast.SelectorExpr:
		if path, ok := st.pkgOf(fun.X); ok {
			return st.g.byKey[path+"."+fun.Sel.Name]
		}
		return st.g.byKey[st.fn.pkg.path+".*."+fun.Sel.Name]
	}
	return nil
}

func (st *funcState) isPath(e ast.Expr) bool {
	if v, ok := st.constString(e, 0); ok && saysManifest(v) {
		return true
	}
	switch e := e.(type) {
	case *ast.Ident:
		return saysManifest(e.Name) || e.Obj != nil && st.path[e.Obj]
	case *ast.BasicLit:
		v, err := strconv.Unquote(e.Value)
		return e.Kind == token.STRING && err == nil && saysManifest(v)
	case *ast.SelectorExpr:
		return saysManifest(e.Sel.Name)
	case *ast.ParenExpr:
		return st.isPath(e.X)
	case *ast.BinaryExpr:
		return st.isPath(e.X) || st.isPath(e.Y)
	case *ast.CallExpr:
		for _, target := range st.resolve(e) {
			if target.returnsPath {
				return true
			}
		}
		for _, arg := range e.Args {
			if st.isPath(arg) {
				return true
			}
		}
	}
	return false
}

var fileReads = map[string]int{
	"os.ReadFile": 0, "os.Open": 0, "os.OpenFile": 0,
	"io/ioutil.ReadFile": 0, "io/fs.ReadFile": 1,
}

func (st *funcState) isContent(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Obj != nil && st.content[e.Obj]
	case *ast.ParenExpr:
		return st.isContent(e.X)
	case *ast.SelectorExpr:
		if _, isPkg := st.pkgOf(e.X); e.Sel.Name == "Manifest" && !isPkg {
			st.source(e)
			return true
		}
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if path, ok := st.pkgOf(sel.X); ok {
				if i, reads := fileReads[path+"."+sel.Sel.Name]; reads && i < len(e.Args) && st.isPath(e.Args[i]) {
					st.source(e)
					return true
				}
				if path == "io" && sel.Sel.Name == "ReadAll" && len(e.Args) == 1 && st.isContent(e.Args[0]) {
					return true
				}
			}
		}
		if st.isConversion(e) {
			return st.isContent(e.Args[0])
		}
		for _, target := range st.resolve(e) {
			if target.returnsContent {
				return true
			}
		}
	}
	return false
}

func (st *funcState) source(n ast.Node) {
	if st.g.record {
		st.g.sources[n.Pos()] = st.fn.pkg.path
	}
}

func (st *funcState) isConversion(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "string" && fun.Obj == nil
	case *ast.ArrayType:
		return fun.Len == nil
	}
	return false
}

func (st *funcState) checkUse(stack []ast.Node) {
	i := len(stack) - 1
	child := stack[i]
	for i > 0 {
		if _, ok := stack[i-1].(*ast.ParenExpr); !ok {
			break
		}
		i--
		child = stack[i]
	}
	if i == 0 {
		return
	}
	switch p := stack[i-1].(type) {
	case *ast.CallExpr:
		if p.Fun == child {
			break
		}
		arg := -1
		for j, a := range p.Args {
			if a == child {
				arg = j
			}
		}
		switch {
		case st.isConversion(p),
			st.isPkgFunc(p.Fun, kitContractPath, "ParseManifest"),
			st.isPkgFunc(p.Fun, "crypto/sha256", "Sum256"),
			st.isPkgFunc(p.Fun, "io", "ReadAll"):
			return
		}
		if id, ok := p.Fun.(*ast.Ident); ok && id.Name == "len" && id.Obj == nil {
			return
		}
		if targets := st.resolve(p); len(targets) > 0 && arg >= 0 {
			for _, target := range targets {
				st.g.mark(target.contentParam, arg)
			}
			return
		}
	case *ast.SelectorExpr:
		if p.X == child && p.Sel.Name == "Close" && i >= 2 {
			if call, ok := stack[i-2].(*ast.CallExpr); ok && call.Fun == p {
				return
			}
		}
	case *ast.BinaryExpr:
		other := p.X
		if other == child {
			other = p.Y
		}
		if id, ok := other.(*ast.Ident); ok && id.Name == "nil" && (p.Op == token.EQL || p.Op == token.NEQ) {
			return
		}
	case *ast.AssignStmt:
		for _, l := range p.Lhs {
			if l == child {
				return
			}
		}
		for j, r := range p.Rhs {
			if r != child {
				continue
			}
			lhs := p.Lhs[0]
			if len(p.Lhs) == len(p.Rhs) {
				lhs = p.Lhs[j]
			}
			if id, ok := lhs.(*ast.Ident); ok && (id.Name == "_" || id.Obj != nil) {
				if id.Obj != nil {
					st.content[id.Obj] = true
				}
				return
			}
		}
	case *ast.ValueSpec:
		for j, v := range p.Values {
			if v == child && j < len(p.Names) && p.Names[j].Obj != nil {
				st.content[p.Names[j].Obj] = true
				return
			}
		}
	case *ast.ReturnStmt:
		if !st.inFuncLit(stack[:i]) {
			if !st.fn.returnsContent {
				st.fn.returnsContent, st.g.changed = true, true
			}
			return
		}
	case *ast.ExprStmt:
		return
	}
	st.violation(child, stack[i-1])
}

func (st *funcState) violation(child, parent ast.Node) {
	if !st.g.record {
		return
	}
	pos := st.g.fset.Position(child.Pos())
	st.g.violations[child.Pos()] = fmt.Sprintf("%s:%d: %s: manifest content (%s) is used in %s, not handed to contract.ParseManifest",
		filepath.Base(pos.Filename), pos.Line, st.fn.decl.Name.Name, nodeText(st.g.fset, child), nodeText(st.g.fset, parent))
}

func nodeText(fset *token.FileSet, n ast.Node) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, n); err != nil {
		return fmt.Sprintf("%T", n)
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	if len(s) > 100 {
		s = s[:100] + "..."
	}
	return s
}

func loadGuardPackage(t *testing.T, fset *token.FileSet, dir, importPath string) *guardPackage {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	p := &guardPackage{path: importPath}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", filepath.Join(dir, name), err)
		}
		p.files = append(p.files, f)
	}
	if len(p.files) == 0 {
		t.Fatalf("%s holds no non-test .go file, so the guard would read nothing", dir)
	}
	return p
}

func TestEveryManifestReadUnderBlockverifyAndHandlerGoesThroughTheParser(t *testing.T) {
	fset := token.NewFileSet()
	pkgs := []*guardPackage{
		loadGuardPackage(t, fset, ".", kitBlockverify),
		loadGuardPackage(t, fset, filepath.Join("..", "handler"), kitHandlerPath),
	}
	sources, violations := guardManifestReads(fset, pkgs)
	for _, p := range pkgs {
		if sources[p.path] == 0 {
			t.Errorf("the guard found no manifest read in %s, so finding no read off the parser there proves nothing", p.path)
		}
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// Written in two pieces so this file does not name the old reader.
var oldReaderName = "read" + "BlockManifest"

func TestNoFileUnderBlockverifyOrHandlerNamesTheOldReader(t *testing.T) {
	files := 0
	for _, dir := range []string{".", filepath.Join("..", "handler")} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files++
			for i, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, oldReaderName) {
					t.Errorf("%s:%d names %s", path, i+1, oldReaderName)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if files == 0 {
		t.Fatal("the scan read no file, so finding nothing proves nothing")
	}
}

type guardCase struct {
	label       string
	blockverify string
	handler     string
	violations  int
}

const guardPreamble = `import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/contract"
	"github.com/mitstack-eu/operator-kit/v2/handler"
)

var _ = bytes.Contains
var _ = sha256.Sum256
var _ = strings.Split
var _ = filepath.Join
var _ = os.ReadFile
var _ = contract.ParseManifest
var _ = handler.Read

const manifestFile = "manifest.yaml"
`

const guardHandlerPreamble = `import (
	"bytes"
	"strings"

	"github.com/mitstack-eu/operator-kit/v2/contract"
)

var _ = bytes.Contains
var _ = strings.Split
var _ = contract.ParseManifest

type Config struct{ Manifest []byte }

func Read(b []byte) int { return len(b) }
`

var guardCases = []guardCase{
	{
		label: "a []byte manifest parameter cut into lines",
		handler: `func ManifestNames(manifest []byte) int {
	return len(strings.Split(string(manifest), "\n"))
}`,
		violations: 1,
	},
	{
		label: "a []byte manifest parameter handed to the parser",
		handler: `func ManifestNames(manifest []byte) error {
	if len(manifest) == 0 || manifest == nil {
		return nil
	}
	_, err := contract.ParseManifest(manifest)
	return err
}`,
	},
	{
		label: "a manifest file read and searched",
		blockverify: `func declared(target string) bool {
	raw, err := os.ReadFile(filepath.Join(target, manifestFile))
	if err != nil {
		return false
	}
	return bytes.Contains(raw, []byte("contract:"))
}`,
		violations: 1,
	},
	{
		label: "a manifest file read, parsed and digested",
		blockverify: `func declared(target string) ([32]byte, error) {
	path := filepath.Join(target, "manifest.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return [32]byte{}, err
	}
	if _, err := contract.ParseManifest(raw); err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}`,
	},
	{
		label: "a manifest file read and scanned by a helper",
		blockverify: `func declared(target string) int {
	path := filepath.Join(target, manifestFile)
	raw, _ := os.ReadFile(path)
	return scan(string(raw))
}

func scan(content string) int {
	n := 0
	for _, line := range strings.Split(content, "\n") {
		n += len(line)
	}
	return n
}`,
		violations: 1,
	},
	{
		label: "a Config.Manifest searched",
		handler: `func New(cfg Config) error {
	if len(cfg.Manifest) == 0 {
		return nil
	}
	if bytes.HasPrefix(cfg.Manifest, []byte("apiVersion")) {
		return nil
	}
	return nil
}`,
		violations: 1,
	},
	{
		label: "a manifest handed to a reader in the other package",
		blockverify: `func declared(target string) int {
	raw, _ := os.ReadFile(filepath.Join(target, manifestFile))
	return handler.Read(raw) + handler.Count(raw)
}`,
		handler:    `func Count(b []byte) int { return bytes.Count(b, []byte("\n")) }`,
		violations: 1,
	},
	{
		label: "a base manifest path passed down and its file trimmed",
		blockverify: `func suite(target, baseManifest string) string { return compare(baseManifest) }

func compare(base string) string {
	raw, _ := os.ReadFile(base)
	return strings.TrimSpace(string(raw))
}`,
		violations: 1,
	},
	{
		label: "a helper that returns the manifest bytes to a reader",
		blockverify: `func readBytes(target string) []byte {
	raw, _ := os.ReadFile(filepath.Join(target, manifestFile))
	return raw
}

func declared(target string) int {
	b := readBytes(target)
	return len(strings.Fields(string(b)))
}`,
		violations: 1,
	},
	{
		label: "a manifest stored in a field",
		handler: `type holder struct{ raw []byte }

func New(cfg Config) *holder {
	h := &holder{}
	h.raw = cfg.Manifest
	return h
}`,
		violations: 1,
	},
	{
		label: "a manifest path written as a constant concatenation",
		blockverify: `func declared(target string) bool {
	raw, _ := os.ReadFile(filepath.Join(target, "mani"+"fest.yaml"))
	return bytes.Contains(raw, []byte("contract:"))
}`,
		violations: 1,
	},
	{
		label: "a manifest path held in a constant built by concatenation",
		blockverify: `const fileName = "mani" + "fest.yaml"

func declared(target string) bool {
	raw, _ := os.ReadFile(filepath.Join(target, fileName))
	return bytes.Contains(raw, []byte("contract:"))
}`,
		violations: 1,
	},
}

func TestManifestReadGuard_CatchesEveryHandReaderAndPassesAParserRead(t *testing.T) {
	for _, tc := range guardCases {
		t.Run(tc.label, func(t *testing.T) {
			fset := token.NewFileSet()
			parse := func(name, pkg, preamble, body string) *ast.File {
				f, err := parser.ParseFile(fset, name, "package "+pkg+"\n\n"+preamble+"\n"+body+"\n", 0)
				if err != nil {
					t.Fatalf("parsing the %s case: %v", name, err)
				}
				return f
			}
			pkgs := []*guardPackage{
				{path: kitBlockverify, files: []*ast.File{parse("blockverify.go", "main", guardPreamble, tc.blockverify)}},
				{path: kitHandlerPath, files: []*ast.File{parse("handler.go", "handler", guardHandlerPreamble, tc.handler)}},
			}
			sources, violations := guardManifestReads(fset, pkgs)
			if len(violations) != tc.violations {
				t.Errorf("the guard reported %d uses off the parser, want %d:\n%s", len(violations), tc.violations, strings.Join(violations, "\n"))
			}
			if sources[kitBlockverify]+sources[kitHandlerPath] == 0 {
				t.Error("the guard found no manifest source in the case")
			}
		})
	}
}
