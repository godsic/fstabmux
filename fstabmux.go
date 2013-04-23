/*
 * fstabmux.go
 *
 * Copyright 2013 Mykola Dvornik <mykola.dvornik@gmail.com>
 *
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 2 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, write to the Free Software
 * Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston,
 * MA 02110-1301, USA.
 *
 *
 */

package fstabmux

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"
)

var chrootMap = map[string]bool{
	"HTTP":  true,
	"HTTPS": true,
	"FTP":   true,
	"SMB":   true,
}

func doChroot(schema string) bool {
	s := strings.ToUpper(schema)
	_, ok := chrootMap[s]
	return ok
}

func getFunctionName(i interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func checkError(err error) {
	if err != nil {
		log.Println(err)
	}
}

func newReverseProxy(target *url.URL, basedir string) *httputil.ReverseProxy {
	targetQuery := target.RawQuery
	lenBasedir := len(basedir)
	director := func(req *http.Request) {
		path := req.URL.Path[lenBasedir:]
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = singleJoiningSlash(target.Path, path)
		if targetQuery == "" || req.URL.RawQuery == "" {
			req.URL.RawQuery = targetQuery + req.URL.RawQuery
		} else {
			req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
		}
	}
	return &httputil.ReverseProxy{Director: director}
}

type mountList struct {
	Fstab           map[string]string // map[HOST]MOUNTPOINT
	handlerFuncPool map[string]http.HandlerFunc
	mux             *http.ServeMux
	desc            string
	updatePeriod    time.Duration
	mutex           sync.RWMutex
}

func (rp *mountList) Mux() *http.ServeMux {
	return rp.mux
}

func (rp *mountList) unmountAllLazy() {
	for i, _ := range rp.Fstab {
		delete(rp.Fstab, i)
	}
	rp.mux = http.NewServeMux()
}

func (rp *mountList) mountAll() {
	rp.HandleFunc("/", rp.df)
	for i, val := range rp.Fstab {
		log.Printf("%s -> %s\n", i, val)
		path, err := url.Parse(i)
		checkError(err)
		switch {
		case doChroot(path.Scheme):
			func() {
				proxy := newReverseProxy(path, val)
				rp.Handle(val, proxy)
			}()
		case path.Scheme == "":
			func() {
				f, ok := rp.handlerFuncPool[i]
				if ok {
					rp.HandleFunc(val, f)
				} else {
					log.Printf("Resource not found: %s\n", i)
				}
			}()
			//~ default:
			//~ func() {
			//~ rp.mux.Handle(val, http.NotFoundHandler())
			//~ }()
		}
	}
}

func (rp *mountList) fetchMountsList() {
	file, e := os.Open(rp.desc)
	defer file.Close()
	checkError(e)
	dec := json.NewDecoder(file)
	e = dec.Decode(&rp)
	checkError(e)
}

func (rp *mountList) updateMountsList() {
	rp.mutex.Lock()
	rp.unmountAllLazy()
	rp.fetchMountsList()
	rp.mountAll()
	log.Println(rp.mux)
	rp.mutex.Unlock()
}

func (rp *mountList) chroot(f func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref := r.Header["Referer"]
		if ref != nil {
			//~ //~
			// Merge referer with requested
			// Fucking golang has no strcmp analog
			// So nested mounting points are not supported
			//~ //~
			ref, _ := url.Parse(r.Header["Referer"][0])
			for i, val := range rp.Fstab {
				targURL, _ := url.Parse(i)
				if doChroot(targURL.Scheme) {
					if strings.Contains(ref.Path, val) {
						ref.Path = singleJoiningSlash(val, r.URL.Path)
						http.Redirect(w, r, ref.String(), http.StatusFound)
					}
				}
			}
		}
		f(w, r)
	}
}

func (rp *mountList) df(w http.ResponseWriter, r *http.Request) {
	for i, val := range rp.Fstab {
		fmt.Fprintf(w, "%s -> %s\n", i, val)
	}
}

func (rp *mountList) dff(w http.ResponseWriter, r *http.Request) {
	for i, val := range rp.Fstab {
		fmt.Fprintf(w, "%s ->->-> %s\n", i, val)
	}
}

func (rp *mountList) HandleFunc(mp string, f func(w http.ResponseWriter, r *http.Request)) {
	ff := f
	if mp == "/" {
		ff = rp.chroot(f)
	}
	rp.mux.HandleFunc(mp, http.HandlerFunc(ff))
}

func (rp *mountList) Handle(mp string, f http.Handler) {
	rp.mux.Handle(mp, f)
}

func (rp *mountList) autoUpdate() {
	go func() {
		for {
			if rp.updatePeriod != time.Duration(0) {
				rp.updateMountsList()
			}
			time.Sleep(rp.updatePeriod)
		}
	}()
}

func (rp *mountList) SetUpdatePeriod(period int64) {
	rp.updatePeriod = time.Duration(period) * time.Second
}

func (rp *mountList) AddHandlerFuncToPool(f func(http.ResponseWriter, *http.Request)) {
	rp.mutex.Lock()
	rp.handlerFuncPool[getFunctionName(f)] = http.HandlerFunc(f)
	rp.mutex.Unlock()
	rp.updateMountsList()
}

func NewJSONServeMux(desc string) (*mountList, error) {
	rp := new(mountList)
	rp.handlerFuncPool = make(map[string]http.HandlerFunc)
	rp.desc = desc
	rp.updateMountsList()
	rp.SetUpdatePeriod(10)
	rp.autoUpdate()
	return rp, nil
}
