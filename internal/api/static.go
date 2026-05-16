package api

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"

	root "github.com/mrgeoffrich/bacio"
)

// webUIFS is the embedded React bundle, rooted at the inner `webui/`
// folder so the index.html sits at the FS root rather than at
// `webui/index.html`. Held as a package-level var so the handler
// closes over it once at startup.
var webUIFS = func() fs.FS {
	sub, err := fs.Sub(root.WebUIFS, "webui")
	if err != nil {
		// A misconfigured embed would surface at server startup
		// instead — handlers can't be created for a missing root.
		// Fall back to an empty FS so the /ui/ handler 404s every
		// path rather than panic.
		return emptyFS{}
	}
	return sub
}()

// hasWebUI reports whether the bundle was populated at build time
// (anything beyond the placeholder .gitkeep). When false, the /ui/
// handler returns a polite 404 page rather than mounting an empty
// SPA shell — useful so an operator who ran `bacio api` without
// running `task build:web` first sees a clear hint.
func hasWebUI() bool {
	f, err := webUIFS.Open("index.html")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// handleUI serves the web bundle at /ui/. It's an SPA, so any
// non-asset path under /ui/ that doesn't resolve to a real file
// falls back to index.html — the React router takes it from there.
//
// The route is registered as `GET /ui/` which ServeMux interprets as
// a prefix match for everything beneath /ui/. A bare GET /ui (no
// trailing slash) lands on the catch-all root mux entry; that's
// handled by 301-redirecting it to /ui/ so the SPA's `base` resolves
// correctly.
func (d deps) handleUI(w http.ResponseWriter, r *http.Request) {
	if !hasWebUI() {
		http.NotFound(w, r)
		_, _ = w.Write([]byte("\nThe bacio web UI bundle hasn't been built into this binary.\n" +
			"Run `npm --prefix desktop/frontend run build:web` and copy\n" +
			"`desktop/frontend/dist-web/` to `webui/` at the repo root,\n" +
			"then rebuild bacio.\n"))
		return
	}

	// Strip the /ui prefix so the file lookup is relative to the FS root.
	rel := strings.TrimPrefix(r.URL.Path, "/ui")
	if rel == "" || rel == "/" {
		rel = "/index.html"
	}
	rel = strings.TrimPrefix(rel, "/")

	// Reject path traversal explicitly. fs.Sub already rejects "..",
	// but clean the path so a request like /ui//assets//foo doesn't
	// trip up the lookup.
	rel = path.Clean(rel)
	if strings.HasPrefix(rel, "..") || rel == "." {
		http.NotFound(w, r)
		return
	}

	f, err := webUIFS.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// SPA fallback — every unknown path under /ui/ resolves
			// to the bundled index.html so the React router can
			// handle it. Asset-shaped paths (with a file extension)
			// still 404 cleanly so missing JS/CSS doesn't return
			// HTML and confuse the browser.
			if path.Ext(rel) == "" {
				rel = "index.html"
				f, err = webUIFS.Open(rel)
			}
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if stat.IsDir() {
		http.NotFound(w, r)
		return
	}

	// http.ServeContent picks Content-Type from the filename and
	// handles If-Modified-Since / Range, which is the right shape
	// for a static asset server. Cast the file to io.ReadSeeker —
	// embed.FS files satisfy it.
	if rs, ok := f.(interface {
		Read([]byte) (int, error)
		Seek(int64, int) (int64, error)
	}); ok {
		http.ServeContent(w, r, rel, stat.ModTime(), rs)
		return
	}
	// Defensive fallback — should never happen for an embed.FS file.
	http.Error(w, "internal", http.StatusInternalServerError)
}

// handleUIRedirect 301-redirects /ui to /ui/ so the SPA's
// `base: '/ui/'` resolves asset paths correctly. Without it a user
// typing `/ui` would get a 404 from the prefix handler.
func (d deps) handleUIRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
}

// emptyFS is the fallback FS used when the embed root can't be
// resolved. Every Open call returns ErrNotExist so the handler
// degrades to clean 404s rather than panicking.
type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
