package server

import (
	"io"
	"log"
	"net/http"
	"runtime"

	"github.com/go-chi/chi/v5/middleware"
)

// newAccessLogger logs each request as chi's middleware.Logger does, but with
// its path alone. A query string can carry what does not belong in a log, such
// as an email address typed into member search.
func newAccessLogger(out io.Writer) func(http.Handler) http.Handler {
	return middleware.RequestLogger(pathOnlyLogFormatter{&middleware.DefaultLogFormatter{
		Logger:  log.New(out, "", log.LstdFlags),
		NoColor: runtime.GOOS == "windows",
	}})
}

type pathOnlyLogFormatter struct {
	middleware.LogFormatter
}

func (f pathOnlyLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	logged := r.WithContext(r.Context())
	logged.RequestURI = r.URL.EscapedPath()
	return f.LogFormatter.NewLogEntry(logged)
}
