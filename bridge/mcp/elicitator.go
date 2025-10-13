package mcp

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viant/jsonrpc"
	protoClient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
)

type Elicitator struct {
	listenAddr  string
	openBrowser bool

	mu       sync.Mutex
	srv      *http.Server
	baseURL  string
	sessions map[string]chan *schema.ElicitResult
}

func NewElicitator(listenAddr string, openBrowser bool) *Elicitator {
	return &Elicitator{listenAddr: listenAddr, openBrowser: openBrowser, sessions: make(map[string]chan *schema.ElicitResult)}
}

func (e *Elicitator) ensureServer(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.srv != nil {
		return nil
	}
	ln, err := net.Listen("tcp", e.listenAddr)
	if err != nil {
		return err
	}
	addr := ln.Addr().String()
	e.baseURL = "http://" + addr

	mux := http.NewServeMux()
	mux.HandleFunc("/elicit", e.handleElicit)
	mux.HandleFunc("/submit", e.handleSubmit)
	mux.HandleFunc("/action", e.handleAction)

	e.srv = &http.Server{Handler: mux}
	go func() {
		_ = e.srv.Serve(ln)
	}()
	// graceful shutdown on ctx cancel
	go func() {
		<-ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = e.srv.Shutdown(ctx)
		cancel()
	}()
	return nil
}

func (e *Elicitator) OpenURL(u string) {
	if !e.openBrowser {
		log.Printf("Elicitator: open %s", u)
		return
	}
	// best-effort open default browser for macOS/Linux/Windows
	cmds := [][]string{{"open", u}, {"xdg-open", u}, {"powershell", "Start-Process", u}}
	for _, c := range cmds {
		if _, err := exec.LookPath(c[0]); err == nil {
			_ = exec.Command(c[0], c[1:]...).Start()
			break
		}
	}
}

func (e *Elicitator) Start(ctx context.Context, req *schema.ElicitRequest) (string, <-chan *schema.ElicitResult, error) {
	if err := e.ensureServer(ctx); err != nil {
		return "", nil, err
	}
	ch := make(chan *schema.ElicitResult, 1)
	e.mu.Lock()
	e.sessions[req.Params.ElicitationId] = ch
	e.mu.Unlock()
	u := fmt.Sprintf("%s/elicit?id=%s", e.baseURL, url.QueryEscape(req.Params.ElicitationId))
	e.OpenURL(u)
	return u, ch, nil
}

func (e *Elicitator) handleElicit(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	message := r.Form.Get("message")
	mode := r.Form.Get("mode")
	link := r.Form.Get("url")
	required := r.Form["required"]
	props := r.Form["prop"]
	page := `<!doctype html><html><head><meta charset="utf-8"><title>Elicitation</title>
<style>body{font-family:system-ui,Arial,sans-serif;margin:2rem} .field{margin:0.5rem 0} label{display:block;font-weight:600} input[type=text],input[type=number]{width:24rem;padding:0.4rem} .actions{margin-top:1rem} a.button,button{padding:0.5rem 0.9rem;margin-right:0.6rem;text-decoration:none;border:1px solid #444;border-radius:6px;background:#f5f5f5;color:#111} .hint{color:#555;font-size:0.9rem}</style>
</head><body>
<h2>Elicitation</h2>
<p>{{.Message}}</p>
{{if eq .Mode "url"}}
  <p>Open: <a class="button" href="{{.Link}}" target="_blank" rel="noopener">{{.Host}}</a></p>
  <form method="post" action="/action">
    <input type="hidden" name="id" value="{{.Id}}"/>
    <button name="act" value="accept">I’ve completed</button>
    <button name="act" value="decline">Decline</button>
    <button name="act" value="cancel">Cancel</button>
  </form>
{{else}}
  <form method="post" action="/submit">
   <input type="hidden" name="id" value="{{.Id}}"/>
   {{range .Props}}
     <div class="field">
       <label for="f_{{.Name}}">{{.Title}}</label>
       {{if eq .Type "boolean"}}
         <input type="checkbox" id="f_{{.Name}}" name="{{.Name}}" value="true" />
       {{else if eq .Type "number"}}
         <input type="number" step="any" id="f_{{.Name}}" name="{{.Name}}"/>
       {{else}}
         <input type="text" id="f_{{.Name}}" name="{{.Name}}"/>
       {{end}}
       {{if .Required}}<div class="hint">Required</div>{{end}}
     </div>
   {{end}}
   {{range .Required}}<input type="hidden" name="required" value="{{.}}"/>{{end}}
   <div class="actions">
     <button type="submit">Submit</button>
     <a class="button" href="/action?act=decline&id={{.Id}}">Decline</a>
     <a class="button" href="/action?act=cancel&id={{.Id}}">Cancel</a>
   </div>
  </form>
{{end}}
</body></html>`
	type prop struct {
		Name, Type, Title string
		Required          bool
	}
	var p []prop
	for _, v := range props {
		parts := strings.Split(v, ":")
		item := prop{}
		if len(parts) > 0 {
			item.Name = parts[0]
		}
		if len(parts) > 1 {
			item.Type = parts[1]
		}
		if len(parts) > 2 {
			item.Title = parts[2]
		} else {
			item.Title = parts[0]
		}
		p = append(p, item)
	}
	reqSet := map[string]bool{}
	for _, r := range required {
		reqSet[r] = true
	}
	for i := range p {
		if reqSet[p[i].Name] {
			p[i].Required = true
		}
	}
	host := ""
	if link != "" {
		if u, err := url.Parse(link); err == nil {
			host = u.Host
		}
	}
	data := struct {
		Id, Message, Mode, Link, Host string
		Props                         []prop
		Required                      []string
	}{Id: id, Message: message, Mode: mode, Link: link, Host: host, Props: p, Required: required}
	tpl := template.Must(template.New("page").Parse(page))
	_ = tpl.Execute(w, data)
}

func (e *Elicitator) handleAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := r.Form.Get("id")
	act := r.Form.Get("act")
	res := &schema.ElicitResult{Action: schema.ElicitResultAction(act)}
	e.finish(id, res)
	fmt.Fprintf(w, "OK: %s", act)
}

func (e *Elicitator) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := r.Form.Get("id")
	required := r.Form["required"]
	reqSet := map[string]bool{}
	for _, v := range required {
		reqSet[v] = true
	}
	missing := make([]string, 0)
	content := map[string]interface{}{}
	for key, vals := range r.PostForm {
		if key == "id" || key == "required" {
			continue
		}
		if len(vals) == 0 {
			continue
		}
		v := vals[0]
		if reqSet[key] && strings.TrimSpace(v) == "" {
			missing = append(missing, key)
		}
		if v == "true" || v == "false" {
			content[key] = (v == "true")
		} else if n, err := parseNumber(v); err == nil {
			content[key] = n
		} else {
			content[key] = v
		}
	}
	for k := range reqSet {
		if _, ok := content[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		http.Error(w, "missing required: "+strings.Join(missing, ", "), http.StatusBadRequest)
		return
	}
	res := &schema.ElicitResult{Action: schema.ElicitResultActionAccept, Content: content}
	e.finish(id, res)
	fmt.Fprintf(w, "OK: submitted")
}

func (e *Elicitator) finish(id string, res *schema.ElicitResult) {
	e.mu.Lock()
	ch, ok := e.sessions[id]
	if ok {
		delete(e.sessions, id)
	}
	e.mu.Unlock()
	if ok {
		ch <- res
		close(ch)
	}
}

func parseNumber(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// opsAugmented wraps Operations and injects a built-in elicitator when needed.
type opsAugmented struct {
	protoClient.Operations
	el *Elicitator
}

func (o *opsAugmented) Implements(method string) bool {
	if o.Operations.Implements(method) {
		return true
	}
	if o.el != nil && method == schema.MethodElicitationCreate {
		return true
	}
	return false
}

func (o *opsAugmented) Init(ctx context.Context, capabilities *schema.ClientCapabilities) {
	o.Operations.Init(ctx, capabilities)
	if o.el != nil {
		if capabilities.Elicitation == nil {
			capabilities.Elicitation = map[string]interface{}{"supported": true}
		}
	}
}

func (o *opsAugmented) Elicit(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ElicitRequest]) (*schema.ElicitResult, *jsonrpc.Error) {
	if o.Operations.Implements(schema.MethodElicitationCreate) || o.el == nil {
		return o.Operations.Elicit(ctx, request)
	}
	// build query for UI rendering
	q := url.Values{}
	q.Set("id", request.Request.Params.ElicitationId)
	q.Set("message", request.Request.Params.Message)
	if request.Request.Params.Mode != "" {
		q.Set("mode", request.Request.Params.Mode)
	}
	if request.Request.Params.Url != "" {
		q.Set("url", request.Request.Params.Url)
	}
	for name, raw := range request.Request.Params.RequestedSchema.Properties {
		typ := "string"
		title := name
		if m, ok := raw.(map[string]interface{}); ok {
			if v, ok := m["type"].(string); ok {
				typ = v
			}
			if v, ok := m["title"].(string); ok {
				title = v
			}
		}
		q.Add("prop", fmt.Sprintf("%s:%s:%s", name, typ, title))
	}
	for _, r := range request.Request.Params.RequestedSchema.Required {
		q.Add("required", r)
	}

	// start listening and open page
	_, done, err := o.el.Start(ctx, request.Request)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	u := fmt.Sprintf("%s/elicit?%s", o.el.baseURL, q.Encode())
	o.el.OpenURL(u)

	select {
	case <-ctx.Done():
		return nil, jsonrpc.NewInternalError(ctx.Err().Error(), nil)
	case res := <-done:
		if res == nil {
			return nil, jsonrpc.NewInternalError("elicitation aborted", nil)
		}
		return res, nil
	}
}
