package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

const basePort = 26257
const dataDir = "cockroach-data"

var cockroachBin = func() string {
	bin := "./cockroach"
	if _, err := os.Stat(bin); err == nil {
		return bin
	}
	return "cockroach"
}()

type cluster struct {
	Nodes      map[string]*node
	NextPort   int
	args       []string
	attrs      perNodeAttribute
	localities perNodeAttribute
}

func newCluster(args []string, attrs, localities perNodeAttribute) *cluster {
	return &cluster{
		Nodes:      map[string]*node{},
		NextPort:   basePort,
		args:       args,
		attrs:      attrs,
		localities: localities,
	}
}

func (c *cluster) close() {
	for _, t := range c.Nodes {
		if t.Active != nil && t.Active.Cmd != nil && t.Active.Cmd.Process != nil {
			t.Active.Cmd.Process.Kill()
		}
	}
}

var envRE = regexp.MustCompile(`(COCKROACH_[^=]+|GO[^=]+)=(.*)`)

func (c *cluster) newNode() *node {
	id := len(c.Nodes) + 1
	name := fmt.Sprintf("%d", id)
	dir := filepath.Join(dataDir, name)
	logdir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logdir, 0755); err != nil {
		log.Fatal(err)
	}

	port := c.NextPort
	httpPort := c.NextPort + 1
	c.NextPort += 2

	args := []string{
		cockroachBin,
		"start",
		"--insecure",
		"--host=localhost",
		fmt.Sprintf("--port=%d", port),
		fmt.Sprintf("--http-port=%d", httpPort),
		fmt.Sprintf("--store=%s", dir),
		fmt.Sprintf("--cache=256MiB"),
		// fmt.Sprintf("--logtostderr"),
	}
	if port != basePort {
		args = append(args, fmt.Sprintf("--join=localhost:%d", basePort))
	}
	attributes, found := c.attrs[id]
	if found {
		args = append(args, fmt.Sprintf("--attrs=%s", attributes))
	}
	locality, found := c.localities[id]
	if found {
		args = append(args, fmt.Sprintf("--locality=%s", locality))
	}
	args = append(args, c.args...)

	env := make(map[string]string)
	for _, val := range os.Environ() {
		m := envRE.FindStringSubmatch(val)
		if m == nil {
			continue
		}
		env[m[1]] = m[2]
	}

	node := newNode(name, args, env, true, filepath.Join(logdir, "${RUN}.stdout"),
		filepath.Join(logdir, "${RUN}.stderr"), attributes, locality)
	node.URL = fmt.Sprintf("http://localhost:%d", httpPort)
	c.Nodes[node.Name] = node
	return node
}

func redirect(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Location", req.Referer())
	rw.WriteHeader(http.StatusFound)
}

func (c *cluster) showCluster(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	data := map[string]interface{}{
		"Title":   "cluster",
		"Page":    "Nodes",
		"Cluster": c,
		"Nodes":   c.Nodes,
	}
	renderLayout(rw, "cluster.html", "layout.html", "Content", data)
}

func (c *cluster) addNode(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	c.newNode()
	redirect(rw, req)
}

func (c *cluster) findNode(rw http.ResponseWriter, args map[string]string) *node {
	id := args["node"]
	t, ok := c.Nodes[id]
	if !ok {
		rw.WriteHeader(http.StatusBadRequest)
		renderError(rw, fmt.Sprintf("node %s not found", id))
		return nil
	}
	return t
}

func (c *cluster) findNodeRun(rw http.ResponseWriter, t *node, args map[string]string) *nodeRun {
	run, err := strconv.Atoi(args["run"])
	if err != nil {
		rw.WriteHeader(http.StatusBadRequest)
		renderError(rw, err.Error())
		return nil
	}
	if run < 0 || run >= len(t.Runs) {
		rw.WriteHeader(http.StatusBadRequest)
		renderError(rw, fmt.Sprintf("run %d of node %s not found", run, t.Name))
		return nil
	}
	return t.Runs[run]
}

func (c *cluster) startNode(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	t := c.findNode(rw, args)
	if t == nil {
		return
	}

	t.Service = true
	t.start()

	redirect(rw, req)
}

func (c *cluster) stopNode(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	t := c.findNode(rw, args)
	if t == nil {
		return
	}

	t.Service = false
	t.stop()

	redirect(rw, req)
}

func (c *cluster) pauseNode(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	t := c.findNode(rw, args)
	if t == nil {
		return
	}

	t.pause()

	redirect(rw, req)
}

func (c *cluster) resumeNode(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	t := c.findNode(rw, args)
	if t == nil {
		return
	}

	t.resume()

	redirect(rw, req)
}

func (c *cluster) startAll(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	for _, t := range c.Nodes {
		t.start()
	}
	redirect(rw, req)
}

func (c *cluster) stopAll(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	for _, t := range c.Nodes {
		t.stop()
	}
	redirect(rw, req)
}

func (c *cluster) pauseAll(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	for _, t := range c.Nodes {
		t.pause()
	}
	redirect(rw, req)
}

func (c *cluster) resumeAll(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	for _, t := range c.Nodes {
		t.resume()
	}
	redirect(rw, req)
}

func (c *cluster) nodeHistory(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	t := c.findNode(rw, args)
	if t == nil {
		return
	}

	data := map[string]interface{}{
		"Title":   "Node",
		"Page":    "History",
		"Cluster": c,
		"Node":    t,
	}

	renderLayout(rw, "node.html", "layout.html", "Content", data)
}

func (c *cluster) nodeRunPage(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	t := c.findNode(rw, args)
	if t == nil {
		return
	}

	run := c.findNodeRun(rw, t, args)
	if run == nil {
		return
	}

	data := map[string]interface{}{
		"Title":   "Node run",
		"Page":    "NodeRun",
		"Cluster": c,
		"Node":    t,
		"NodeRun": run,
	}

	renderLayout(rw, "run.html", "layout.html", "Content", data)
}

func (c *cluster) nodeRunStdout(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	t := c.findNode(rw, args)
	if t == nil {
		return
	}

	run := c.findNodeRun(rw, t, args)
	if run == nil {
		return
	}

	data := map[string]interface{}{
		"Title":     "Node run stdout",
		"Page":      "NodeOutput",
		"Type":      "stdout",
		"Cluster":   c,
		"Node":      t,
		"NodeRun":   run,
		"LogOutput": run.StdoutBuf.String(),
	}

	renderLayout(rw, "log.html", "layout.html", "Content", data)
}

func (c *cluster) nodeRunStderr(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	t := c.findNode(rw, args)
	if t == nil {
		return
	}

	run := c.findNodeRun(rw, t, args)
	if run == nil {
		return
	}

	data := map[string]interface{}{
		"Title":     "Node run stderr",
		"Page":      "NodeOutput",
		"Type":      "stderr",
		"Cluster":   c,
		"Node":      t,
		"NodeRun":   run,
		"LogOutput": run.StderrBuf.String(),
	}

	renderLayout(rw, "log.html", "layout.html", "Content", data)
}

func (c *cluster) AnyNodesStarted() bool {
	for _, t := range c.Nodes {
		if t.Active != nil {
			return true
		}
	}
	return false
}

func (c *cluster) AnyNodesStopped() bool {
	for _, t := range c.Nodes {
		if t.Active == nil {
			return true
		}
	}
	return false
}

func (c *cluster) AnyNodesPaused() bool {
	for _, t := range c.Nodes {
		if t.Active != nil {
			if t.Active.Paused {
				return true
			}
		}
	}
	return false
}

func (c *cluster) AnyNodesNotPaused() bool {
	for _, t := range c.Nodes {
		if t.Active != nil {
			if !t.Active.Paused {
				return true
			}
		}
	}
	return false
}
