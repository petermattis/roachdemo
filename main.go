package main

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

var tmpls = map[string]*template.Template{}

func render(asset string, data map[string]interface{}) (string, error) {
	t, ok := tmpls[asset]
	if !ok {
		return "", fmt.Errorf("%s not found", asset)
	}

	var b bytes.Buffer
	err := t.Execute(&b, data)
	if err != nil {
		log.Printf("failed executing template %s: %s", asset, err)
		return "", err
	}

	return b.String(), nil
}

func renderSimple(rw http.ResponseWriter, asset string, data map[string]interface{}) {
	html, err := render(asset, data)
	if err != nil {
		log.Fatal(err)
	}
	_, err = rw.Write([]byte(html))
	if err != nil {
		log.Print(err)
	}
}

func renderError(rw http.ResponseWriter, message string) {
	renderSimple(rw, "error.html", map[string]interface{}{"Error": message})
}

func renderLayout(rw http.ResponseWriter, asset string, layout string, key string,
	data map[string]interface{}) {
	html, err := render(asset, data)
	if err != nil {
		log.Fatal(err)
	}
	data[key] = template.HTML(html)
	renderSimple(rw, layout, data)
}

type routeFn func(rw http.ResponseWriter, req *http.Request, args map[string]string)

type route struct {
	re *regexp.Regexp
	fn routeFn
}

func makeRoute(s string, fn routeFn) route {
	return route{
		re: regexp.MustCompile("^" + s + "$"),
		fn: fn,
	}
}

type routes []route

func (routes routes) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	path := req.URL.Path
	for _, r := range routes {
		m := r.re.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		args := map[string]string{}
		names := r.re.SubexpNames()
		for i := range names {
			if n := names[i]; n != "" {
				args[n] = m[i]
			}
		}
		r.fn(rw, req, args)
		return
	}

	rw.WriteHeader(http.StatusNotFound)
	renderSimple(rw, "notfound.html", nil)
}

func getCSS(rw http.ResponseWriter, req *http.Request, args map[string]string) {
	asset, err := Asset("assets" + req.URL.Path)
	if err != nil {
		log.Print(err)
		rw.WriteHeader(http.StatusNotFound)
		renderError(rw, fmt.Sprintf("assets%s not found", req.URL.Path))
		return
	}
	rw.Header().Add("Content-Type", "text/css")
	_, err = rw.Write(asset)
	if err != nil {
		log.Print(err)
		return
	}
}

func main() {
	for _, path := range AssetNames() {
		if !strings.HasSuffix(path, ".html") {
			continue
		}
		t := template.New(path)
		asset, err := Asset(path)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := t.Parse(string(asset)); err != nil {
			log.Fatal(err)
		}
		tmpls[filepath.Base(path)] = t
	}

	c := newCluster()
	defer c.close()

	paths, _ := filepath.Glob("data/*")
	for range paths {
		c.newNode()
	}

	routes := routes{
		makeRoute(`/`, c.showCluster),
		makeRoute(`/add`, c.addNode),

		makeRoute(`/node/(?P<node>[^/]+)/start`, c.startNode),
		makeRoute(`/node/(?P<node>[^/]+)/stop`, c.stopNode),
		makeRoute(`/node/(?P<node>[^/]+)/pause`, c.pauseNode),
		makeRoute(`/node/(?P<node>[^/]+)/resume`, c.resumeNode),

		makeRoute(`/node/(?P<node>[^/]+)`, c.nodeHistory),
		makeRoute(`/node/(?P<node>[^/]+)/run/(?P<run>\d+)`, c.nodeRunPage),
		makeRoute(`/node/(?P<node>[^/]+)/run/(?P<run>\d+)/stdout`, c.nodeRunStdout),
		makeRoute(`/node/(?P<node>[^/]+)/run/(?P<run>\d+)/stderr`, c.nodeRunStderr),

		makeRoute(`/css/(?P<file>.*)`, getCSS),
	}

	s := &http.Server{
		Addr:    "localhost:9999",
		Handler: routes,
	}
	log.Printf("serving: http://%s", s.Addr)
	if err := s.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
