package control

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// consoleCSP keeps the console to its own origin: no third-party scripts, no framing.
const consoleCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; " +
	"connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// console serves the built operator console from dir. Paths that are not files get index.html,
// so the console's own routes work on reload. The files are public; everything they do goes
// through the token-protected API.
func console(dir string) http.Handler {
	root, _ := filepath.Abs(dir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", consoleCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")

		clean := path.Clean("/" + r.URL.Path)
		name := filepath.Join(root, filepath.FromSlash(clean))
		if !strings.HasPrefix(name, root) {
			http.NotFound(w, r)
			return
		}
		fi, err := os.Stat(name)
		if err != nil || fi.IsDir() {
			name = filepath.Join(root, "index.html")
			fi, err = os.Stat(name)
			if err != nil {
				http.Error(w, "the console is not built", http.StatusNotFound)
				return
			}
		}
		f, err := os.Open(name)
		if err != nil {
			http.Error(w, "the console is not built", http.StatusNotFound)
			return
		}
		defer f.Close()
		if strings.HasPrefix(clean, "/assets/") && name != filepath.Join(root, "index.html") {
			// Vite puts a content hash in every asset name.
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	})
}

// demoProxy forwards /demo/... to a demo traffic driver. The admin token is checked first and
// is not passed on.
func demoProxy(target string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	return &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(u)
		pr.Out.URL.Path = strings.TrimSuffix(u.Path, "/") + strings.TrimPrefix(pr.In.URL.Path, "/demo")
		pr.Out.URL.RawPath = ""
		pr.Out.Header.Del("Authorization")
		pr.Out.Header.Del("Cookie")
	}}, nil
}
