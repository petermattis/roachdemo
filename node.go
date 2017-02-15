package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type node struct {
	Name     string
	Args     []string
	Env      map[string]string
	Stdout   string
	Stderr   string
	URL      string
	Attrs    string
	Locality string

	Active *nodeRun
	Runs   []*nodeRun

	Service bool
}

type nodeRun struct {
	ID         int
	Cmd        *exec.Cmd
	Error      error
	Started    time.Time
	Stopped    time.Time
	Args       []string
	Attrs      string
	Locality   string
	Stdout     string
	Stderr     string
	StdoutBuf  logWriter
	StderrBuf  logWriter
	Env        map[string]string
	WaitStatus syscall.WaitStatus
	Paused     bool
}

func (r *nodeRun) String() string {
	return fmt.Sprintf("Pid %d", r.Cmd.Process.Pid)
}

func (r *nodeRun) Command() string {
	return strings.Join(r.Args, " ")
}

func (r *nodeRun) start(exitCh chan struct{}) {
	r.Started = time.Now()

	if len(r.Stdout) > 0 {
		wr, err := newFileLogWriter(r.Stdout)
		if err != nil {
			log.Fatalf("unable to open file %s: %s", r.Stdout, err.Error())
		}
		r.StdoutBuf = wr
	}
	r.Cmd.Stdout = r.StdoutBuf

	if len(r.Stderr) > 0 {
		wr, err := newFileLogWriter(r.Stderr)
		if err != nil {
			log.Fatalf("unable to open file %s: %s", r.Stderr, err.Error())
		}
		r.StderrBuf = wr
	}
	r.Cmd.Stderr = r.StderrBuf

	for k, v := range r.Env {
		r.Cmd.Env = append(r.Cmd.Env, k+"="+v)
	}

	err := r.Cmd.Start()
	if r.Cmd.Process != nil {
		log.Printf("process %d started: %s", r.Cmd.Process.Pid, strings.Join(r.Args, " "))
	}
	if err != nil {
		r.Error = err
		log.Printf(err.Error())
		r.StdoutBuf.Close()
		r.StderrBuf.Close()
		exitCh <- struct{}{}
		return
	}
	go func() {
		r.Cmd.Wait()

		r.StdoutBuf.Close()
		r.StderrBuf.Close()

		ps := r.Cmd.ProcessState
		sy := ps.Sys().(syscall.WaitStatus)

		log.Printf("Process %d exited with status %d", ps.Pid(), sy.ExitStatus())
		log.Printf(ps.String())

		r.Stopped = time.Now()
		exitCh <- struct{}{}
	}()
}

func (r *nodeRun) stop() {
	if r.Cmd == nil || r.Cmd.Process == nil {
		return
	}

	r.Paused = false
	r.Cmd.Process.Kill()
}

func (r *nodeRun) pause() {
	if r.Cmd == nil || r.Cmd.Process == nil {
		return
	}

	r.Paused = true
	r.Cmd.Process.Signal(syscall.SIGSTOP)
}

func (r *nodeRun) resume() {
	if r.Cmd == nil || r.Cmd.Process == nil {
		return
	}

	r.Paused = false
	r.Cmd.Process.Signal(syscall.SIGCONT)
}

type logWriter interface {
	Write(p []byte) (n int, err error)
	String() string
	Len() int64
	Close()
}

type fileLogWriter struct {
	filename string
	file     *os.File
}

func newFileLogWriter(file string) (*fileLogWriter, error) {
	f, err := os.Create(file)
	if err != nil {
		return nil, err
	}

	return &fileLogWriter{
		filename: file,
		file:     f,
	}, nil
}

func (w fileLogWriter) Close() {
	w.file.Close()
}

func (w fileLogWriter) Write(p []byte) (n int, err error) {
	return w.file.Write(p)
}

func (w fileLogWriter) String() string {
	b, err := ioutil.ReadFile(w.filename)
	if err == nil {
		return string(b)
	}
	return ""
}

func (w fileLogWriter) Len() int64 {
	s, err := os.Stat(w.filename)
	if err == nil {
		return s.Size()
	}
	return 0
}

func newNode(
	name string,
	args []string,
	env map[string]string,
	service bool,
	stdout string,
	stderr string,
	attributes string,
	locality string,
) *node {
	if env == nil {
		env = map[string]string{}
	}
	env = addDefaultVars(env)

	stdout = replaceVars(stdout, env)
	stderr = replaceVars(stderr, env)

	n := &node{
		Name:     name,
		Args:     args,
		Env:      env,
		Runs:     make([]*nodeRun, 0),
		Service:  service,
		Stdout:   stdout,
		Stderr:   stderr,
		Attrs:    attributes,
		Locality: locality,
	}

	if n.Service {
		n.start()
	}

	return n
}

func (n *node) Command() string {
	return strings.Join(n.Args, " ")
}

func (n *node) start() {
	if n.Active != nil {
		return
	}

	run := len(n.Runs)

	args := append([]string(nil), n.Args...)
	for i := range args {
		args[i] = replaceVars(args[i], n.Env)
	}

	cmd := exec.Command(args[0], args[1:]...)

	vars := map[string]string{
		"RUN": strconv.Itoa(run),
	}
	stdout := replaceVars(n.Stdout, vars)
	stderr := replaceVars(n.Stderr, vars)

	n.Active = &nodeRun{
		ID:     run,
		Cmd:    cmd,
		Args:   args,
		Env:    n.Env,
		Stdout: stdout,
		Stderr: stderr,
	}
	n.Runs = append(n.Runs, n.Active)

	c := make(chan struct{})
	n.Active.start(c)
	go func() {
		<-c
		n.Active = nil
		if n.Service {
			time.Sleep(time.Second * 1)
			n.start()
			return
		}
	}()
}

func (n *node) stop() {
	if n.Active != nil {
		n.Active.stop()
		n.Active = nil
	}
}

func (n *node) pause() {
	if n.Active != nil {
		n.Active.pause()
	}
}

func (n *node) resume() {
	if n.Active != nil {
		n.Active.resume()
	}
}

func (n *node) Status() string {
	if n.Active != nil && n.Active.Cmd != nil &&
		n.Active.Cmd.Process != nil && n.Active.Cmd.Process.Pid > 0 {
		if n.Active.Paused {
			return "Paused"
		}
		return "Running"
	}
	return "Stopped"
}

func addDefaultVars(vars map[string]string) map[string]string {
	u, err := user.Current()
	if err == nil {
		if _, ok := vars["USER"]; !ok {
			vars["USER"] = u.Username
		}
		if _, ok := vars["UID"]; !ok {
			vars["UID"] = u.Uid
		}
		if _, ok := vars["GID"]; !ok {
			vars["GID"] = u.Gid
		}
		if _, ok := vars["HOME"]; !ok {
			vars["HOME"] = u.HomeDir
		}
	}
	if _, ok := vars["PATH"]; !ok {
		vars["PATH"] = os.Getenv("PATH")
	}
	return vars
}

func replaceVars(text string, vars map[string]string) string {
	return os.Expand(text, func(name string) string {
		v, ok := vars[name]
		if ok {
			return v
		}
		return "$" + name
	})
}
