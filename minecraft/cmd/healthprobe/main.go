// Command healthprobe is the HEALTHCHECK for the distroless images, which have
// no shell, curl or wget to probe with.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	env := flag.String("env", "HTTP_ADDR", "environment variable holding the service's listen address")
	def := flag.String("default", ":8080", "listen address when the variable is unset")
	path := flag.String("path", "/healthz", "path to request")
	flag.Parse()

	if err := probe(addr(os.Getenv(*env), *def), *path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// addr turns a listen address into one that can be dialled: a bare ":8080" or
// a wildcard host means "this container".
func addr(listen, def string) string {
	if listen == "" {
		listen = def
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func probe(hostport, path string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + hostport + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: status %d", path, resp.StatusCode)
	}
	return nil
}
