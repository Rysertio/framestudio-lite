package store

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Shaitrish/37studio/internal/config"
)

// Backup streams a zip of the data folder: a consistent copy of the database, secret.key (without it the saved keys
// and platform logins can't be read), brands, templates and projects. Project footage and exports (input/, out/)
// come only with ?media=1, since they are most of the size. The Codex login stays out: it belongs to this machine.
// To restore, stop 37Studio and unzip into an empty data folder.
func Backup(w http.ResponseWriter, r *http.Request) {
	media := r.URL.Query().Get("media") == "1"
	// Its own name, so two downloads at once don't share one copy; vacuum into wants a path that doesn't exist yet.
	snap := filepath.Join(config.DataDir, fmt.Sprintf(".backup-%d.db", time.Now().UnixNano()))
	if _, err := DB.Exec("vacuum into ?", snap); err != nil {
		http.Error(w, "couldn't copy the database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.Remove(snap)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="37studio-backup-%s.zip"`, time.Now().Format("2006-01-02")))
	zw := zip.NewWriter(w)
	defer zw.Close()
	add := func(src, name string) error {
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		info, _ := f.Stat()
		h, _ := zip.FileInfoHeader(info)
		h.Name, h.Method = name, zip.Store // videos and images are compressed already
		if strings.HasSuffix(name, ".db") || strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".json") {
			h.Method = zip.Deflate
		}
		out, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, f)
		return err
	}
	if add(snap, "37studio.db") != nil {
		return
	}
	add(filepath.Join(config.DataDir, "secret.key"), "secret.key")
	for _, dir := range []string{"tenants", "templates", "projects"} {
		root := filepath.Join(config.DataDir, dir)
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(config.DataDir, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if parts := strings.Split(rel, "/"); !media && dir == "projects" && len(parts) == 3 && (parts[2] == "input" || parts[2] == "out") {
					return filepath.SkipDir
				}
				return nil
			}
			if err := add(p, rel); err != nil && r.Context().Err() != nil {
				return r.Context().Err() // the browser gave up on the download
			}
			return nil
		})
	}
}
