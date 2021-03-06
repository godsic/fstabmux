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
	"HTTPS": false,
	"FTP":   false,
	"SMB":   false,
}

func doChroot(schema string) bool {
	s := strings.ToUpper(schema)
	val, ok := chrootMap[s]
	return (val && ok)
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

type MountList struct {
	Fstab           map[string]string // map[HOST]MOUNTPOINT
	handlerFuncPool map[string]http.HandlerFunc
	mux             *http.ServeMux
	desc            string
	updatePeriod    time.Duration
	mutex           sync.RWMutex
}

func (m *MountList) Mux() *http.ServeMux {
	return m.mux
}

func (m *MountList) unmountAllLazy() {
	for i, _ := range m.Fstab {
		delete(m.Fstab, i)
	}
	m.mux = nil
}

func (m *MountList) mountAll() {
	m.mux = http.NewServeMux()
	rootSet := false
	for i, val := range m.Fstab {
		if val == "/" {
			rootSet = true
		}
		log.Printf("%s -> %s\n", i, val)
		path, err := url.Parse(i)
		checkError(err)
		switch {
		case doChroot(path.Scheme):
			func() {
				proxy := newReverseProxy(path, val)
				m.handle(val, proxy)
			}()
		case path.Scheme == "":
			func() {
				f, ok := m.handlerFuncPool[i]
				if ok {
					m.handleFunc(val, f)
				} else {
					log.Printf("Resource not found: %s\n", i)
				}
			}()
		default:
			func() {
				m.mux.Handle(val, http.NotFoundHandler())
			}()
		}
	}
	if !rootSet {
		m.handleFunc("/", nil)
	}

}

func (m *MountList) fetchMountsList() {
	file, e := os.Open(m.desc)
	defer file.Close()
	checkError(e)
	dec := json.NewDecoder(file)
	e = dec.Decode(&m)
	checkError(e)
}

func (m *MountList) updateMountsList() {
	m.mutex.Lock()
	m.unmountAllLazy()
	m.fetchMountsList()
	m.mountAll()
	m.mutex.Unlock()
}

func (m *MountList) chroot(w http.ResponseWriter, r *http.Request) {
	ref := r.Header["Referer"]
	if ref != nil {
		//~ //~
		// Merge referer with requested
		// Fucking golang has no strcmp analog
		// So nested mounting points are not supported
		//~ //~
		ref, _ := url.Parse(r.Header["Referer"][0])
		for i, val := range m.Fstab {
			targURL, _ := url.Parse(i)
			if doChroot(targURL.Scheme) {
				if strings.Contains(ref.Path, val) {
					ref.Path = singleJoiningSlash(val, r.URL.Path)
					http.Redirect(w, r, ref.String(), http.StatusFound)
				}
			}
		}
	}
}

func (m *MountList) chrootClosure(f func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	if f == nil {
		return func(w http.ResponseWriter, r *http.Request) {
			m.chroot(w, r)
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		m.chroot(w, r)
		f(w, r)
	}
}

func (m *MountList) handleFunc(mp string, f func(w http.ResponseWriter, r *http.Request)) {
	ff := f
	if mp == "/" {
		ff = m.chrootClosure(f)
	}
	m.mux.HandleFunc(mp, http.HandlerFunc(ff))
}

func (m *MountList) handle(mp string, f http.Handler) {
	m.mux.Handle(mp, f)
}

func (m *MountList) autoUpdate() {
	go func() {
		for {
			fi, e := os.Lstat(m.desc)
			checkError(e)
			lastupdate := time.Since(fi.ModTime())
			if m.updatePeriod != time.Duration(0) && lastupdate < m.updatePeriod {
				m.updateMountsList()
			}
			time.Sleep(m.updatePeriod)
		}
	}()
}

func (m *MountList) SetUpdatePeriod(period int64) {
	m.updatePeriod = time.Duration(period) * time.Second
}

func (m *MountList) AddHandlerFuncToPool(f func(http.ResponseWriter, *http.Request)) {
	m.mutex.Lock()
	m.handlerFuncPool[getFunctionName(f)] = http.HandlerFunc(f)
	m.mutex.Unlock()
	m.updateMountsList()
}

func NewFstabServeMux(desc string) (*MountList, error) {
	m := new(MountList)
	m.handlerFuncPool = make(map[string]http.HandlerFunc)
	m.desc = desc
	m.updateMountsList()
	m.SetUpdatePeriod(10)
	m.autoUpdate()
	return m, nil
}
