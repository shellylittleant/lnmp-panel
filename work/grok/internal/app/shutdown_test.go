package app

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestShutdownHTTPUnblocksLongRequest(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(200)
	})
	srv := &http.Server{
		Handler:     mux,
		BaseContext: func(net.Listener) context.Context { return base },
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	done := make(chan struct{})
	go func() {
		_, _ = http.Get("http://" + ln.Addr().String() + "/hang")
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	shutdownHTTP(srv, cancel)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("长连接未随关闭结束")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("关闭过慢")
	}
}
