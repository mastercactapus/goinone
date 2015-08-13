package main

import (
	"encoding/json"
	"fmt"
	"github.com/bradfitz/http2"
	"github.com/julienschmidt/httprouter"
	log "github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type ctx struct {
	log       *log.Entry
	start     time.Time
	responded bool
	status    int
	w         http.ResponseWriter
}
type stat struct {
	IsDir   bool
	ModTime time.Time
	Name    string
	Mode    os.FileMode
	Size    int64
}

func NewStat(info os.FileInfo) *stat {
	return &stat{
		Name:    info.Name(),
		Mode:    info.Mode(),
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime(),
	}
}

func NewCtx(req *http.Request) *ctx {
	c := new(ctx)
	c.start = time.Now()
	c.log = log.WithFields(log.Fields{
		"Path":   req.URL.Path,
		"Method": req.Method,
		"Remote": req.RemoteAddr,
	})
	return c
}
func (c *ctx) finish() {
	c.log = c.log.WithFields(log.Fields{
		"ResponseTime": time.Since(c.start),
		"StatusCode":   c.status,
	})
	c.log.Infoln("request complete")
}
func (c *ctx) Error(w http.ResponseWriter, code int, err error) {
	c.log.Warnln(err)
	c.status = code
	c.responded = true
	w.WriteHeader(code)
	c.finish()
}
func (c *ctx) Done() {
	if !c.responded {
		c.responded = true
		c.status = 200
		c.finish()
	}
}

func fsRead(w http.ResponseWriter, req *http.Request, pm httprouter.Params) {
	c := NewCtx(req)
	defer c.Done()

	file := filepath.Join(".", pm.ByName("filepath"))

	info, err := os.Stat(file)
	if err != nil {
		if os.IsNotExist(err) {
			c.Error(w, 404, err)
		} else {
			c.Error(w, 500, err)
		}
		return
	}

	if req.URL.Query().Get("stat") == "1" {
		stat := NewStat(info)
		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(&stat)
		if err != nil {
			c.Error(w, 500, err)
			return
		}
		return
	}

	if info.IsDir() {
		infos, err := ioutil.ReadDir(file)
		if err != nil {
			c.Error(w, 500, err)
			return
		}
		info := make([]stat, len(infos))
		for i := range infos {
			info[i] = *NewStat(infos[i])
		}
		w.Header().Set("Content-Type", "application/json")
		err = json.NewEncoder(w).Encode(info)
		if err != nil {
			c.Error(w, 500, err)
			return
		}
		return
	}

	fd, err := os.Open(file)
	if err != nil {
		c.Error(w, 500, err)
		return
	}
	defer fd.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	_, err = io.Copy(w, fd)
	if err != nil {
		c.log.Errorln("copy operation failed:", err)
	}
}
func fsWrite(w http.ResponseWriter, req *http.Request, pm httprouter.Params) {
	c := NewCtx(req)
	defer c.Done()

	file := filepath.Join(".", pm.ByName("filepath"))

	if req.URL.Query().Get("dir") == "1" {
		err := os.Mkdir(file, 0755)
		if err != nil {
			c.Error(w, 500, err)
			return
		}
		return
	}

	fd, err := os.Create(file)
	if err != nil {
		c.Error(w, 500, err)
		return
	}
	defer fd.Close()
	_, err = io.Copy(fd, req.Body)
	if err != nil {
		c.log.Errorln("copy operation failed:", err)
	}
}
func fsDelete(w http.ResponseWriter, req *http.Request, pm httprouter.Params) {
	c := NewCtx(req)
	defer c.Done()

	file := filepath.Join(".", pm.ByName("filepath"))

	_, err := os.Stat(file)
	if err != nil {
		if os.IsNotExist(err) {
			c.Error(w, 404, err)
		} else {
			c.Error(w, 500, err)
		}
		return
	}

	err = os.RemoveAll(file)
	if err != nil {
		c.Error(w, 500, err)
		return
	}
}
func fsSpecial(w http.ResponseWriter, req *http.Request, pm httprouter.Params) {
	c := NewCtx(req)
	defer c.Done()

	file := filepath.Join(".", pm.ByName("filepath"))

	_, err := os.Stat(file)
	if err != nil {
		if os.IsNotExist(err) {
			c.Error(w, 404, err)
		} else {
			c.Error(w, 500, err)
		}
		return
	}

	if req.URL.Query().Get("rename") != "1" {
		c.Error(w, 400, fmt.Errorf("invalid or unknown operation"))
		return
	}

	newBase := req.URL.Query().Get("newbasename")
	if newBase == "" {
		c.Error(w, 400, fmt.Errorf("newbasename is required"))
		return
	}
	newPath := filepath.Join(filepath.Dir(file), newBase)
	err = os.Rename(file, newPath)

	if err != nil {
		c.Error(w, 500, err)
		return
	}
}

func index(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	w.Header().Set("Location", "webapp")
	w.WriteHeader(302)
}

func main() {
	r := httprouter.New()
	r.GET("/", index)
	r.ServeFiles("/webapp/*filepath", http.Dir("webapp"))
	r.GET("/api/fs/*filepath", fsRead)
	r.PUT("/api/fs/*filepath", fsWrite)
	r.DELETE("/api/fs/*filepath", fsDelete)
	r.POST("/api/fs/*filepath", fsSpecial)

	s := &http.Server{}
	s.Addr = ":8080"
	s.Handler = r
	http2.ConfigureServer(s, nil)

	log.Fatalln(s.ListenAndServeTLS("cert.pem", "key.pem"))
}
