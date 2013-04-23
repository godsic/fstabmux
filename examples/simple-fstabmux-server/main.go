/*
 * main.go
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

package main

import (
	"fstabmux"
	"log"
	"net/http"
	"time"
	//~ "fmt"
	"io"
)

const FstabFile = "./fstab/fstab.json"
const ServerAddr = ":8080"

func Boobies(w http.ResponseWriter, r *http.Request) {
	resp, _ := http.Get("http://www.textfiles.com/art/ASCIIPR0N/pinup10.txt")
	io.Copy(w, resp.Body)
}


func main() {
	
	jsonMux, _ := fstabmux.NewJSONServeMux(FstabFile)	
	jsonMux.AddHandlerFuncToPool(Boobies)
	
	server := &http.Server{
		Addr:           ServerAddr,
		Handler:        jsonMux.Mux(),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	log.Fatal(server.ListenAndServe())
}
