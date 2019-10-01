package util 

import (
	"fmt"
	"net"
	"net/http"
	"time"
	"io/ioutil"
	"bytes"
)

//StartProxy creates an HTTP listener to forward traffic to server
func StartProxy(server string) {
	 http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpClient := http.Client{}
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		r.Body = ioutil.NopCloser(bytes.NewReader(body))
		url := server + r.RequestURI

		proxyReq, err := http.NewRequest(r.Method, url, bytes.NewReader(body))
		if err != nil {
			fmt.Println(err.Error())
			return
		}
		proxyReq.Header = make(http.Header)
		for h, val := range r.Header {
			proxyReq.Header[h] = val
		}
		resp, err := httpClient.Do(proxyReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		bites, _ := ioutil.ReadAll(resp.Body)
		w.Write(bites)
	})
	http.ListenAndServe(":8889", nil)
}

//FindProxy locates a useable host for coms
func FindProxy(port string) string {
	for _, ip := range [...]string{"127.0.0.1"}{
		connected := testConnection(ip, port)
		if connected {
			proxy := fmt.Sprintf("http://%s:%s", ip, port)
			fmt.Println("Located available proxy server", proxy)
			return proxy
		}
	}
	return ""
}

func testConnection(ip string, port string) bool {
	conn, _ := net.DialTimeout("tcp", net.JoinHostPort(ip, port), time.Second)
	if conn != nil {
		defer conn.Close()
		return true
	}
	return false
}