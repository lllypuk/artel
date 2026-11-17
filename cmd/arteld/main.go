// arteld is the local Artel API daemon.
package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/lllypuk/artel/internal/api"
)

func main() {
	server := &http.Server{Addr: "127.0.0.1:7331", Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "arteld: unable to serve local API")
		os.Exit(1)
	}
}
