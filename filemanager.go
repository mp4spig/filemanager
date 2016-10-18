//go:generate go get github.com/jteeuwen/go-bindata
//go:generate go install github.com/jteeuwen/go-bindata/go-bindata
//go:generate go-bindata -pkg assets -ignore .jsbeautifyrc -prefix "assets/embed" -o assets/binary.go assets/embed/...

// Package filemanager provides middleware for managing files in a directory
// when directory path is requested instead of a specific file. Based on browse
// middleware.
package filemanager

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hacdias/caddy-filemanager/assets"
	"github.com/hacdias/caddy-filemanager/config"
	"github.com/hacdias/caddy-filemanager/directory"
	"github.com/hacdias/caddy-filemanager/errors"
	"github.com/hacdias/caddy-filemanager/page"
	"github.com/mholt/caddy/caddyhttp/httpserver"
)

// FileManager is an http.Handler that can show a file listing when
// directories in the given paths are specified.
type FileManager struct {
	Next    httpserver.Handler
	Configs []config.Config
}

// ServeHTTP determines if the request is for this plugin, and if all prerequisites are met.
func (f FileManager) ServeHTTP(w http.ResponseWriter, r *http.Request) (int, error) {
	var (
		c           *config.Config
		fi          *directory.Info
		code        int
		err         error
		serveAssets bool
		user        *config.User
	)

	for i := range f.Configs {
		if httpserver.Path(r.URL.Path).Matches(f.Configs[i].BaseURL) {
			c = &f.Configs[i]
			serveAssets = httpserver.Path(r.URL.Path).Matches(c.BaseURL + assets.BaseURL)
			username, _, _ := r.BasicAuth()

			if _, ok := c.Users[username]; ok {
				user = c.Users[username]
			} else {
				user = c.User
			}

			// TODO: make allow and block rules relative to baseurl and webdav
			// Checks if the user has permission to access the current directory.
			/*if !user.Allowed(r.URL.Path) {
				if r.Method == http.MethodGet {
					return errors.PrintHTML(w, http.StatusForbidden, e.New("You don't have permission to access this page."))
				}

				return http.StatusForbidden, nil
			}

			// TODO: How to exclude web dav clients? :/
			// Security measures against CSRF attacks.
			if r.Method != http.MethodGet {
				if !c.CheckToken(r) {
					return http.StatusForbidden, nil
				}
			} */

			if strings.HasPrefix(r.URL.Path, c.WebDavURL) {
				fmt.Println("e")

				switch r.Method {
				case "PROPPATCH", "MOVE", "PATCH", "PUT", "DELETE":
					if !user.AllowEdit {
						return http.StatusForbidden, nil
					}
				case "MKCOL", "COPY":
					if !user.AllowNew {
						return http.StatusForbidden, nil
					}
				}

				if r.Method == http.MethodPut {
					_, err = fi.Update(w, r, c, user)
					if err != nil {
						return http.StatusInternalServerError, err
					}
				}

				c.WebDavHandler.ServeHTTP(w, r)
				return 0, nil
			}

			if r.Method == http.MethodGet && serveAssets {
				return assets.Serve(w, r, c)
			}

			if r.Method == http.MethodGet {
				// Gets the information of the directory/file
				fi, code, err = directory.GetInfo(r.URL, c, user)
				if err != nil {
					if r.Method == http.MethodGet {
						return errors.PrintHTML(w, code, err)
					}
					return code, err
				}

				// If it's a dir and the path doesn't end with a trailing slash,
				// redirect the user.
				if fi.IsDir && !strings.HasSuffix(r.URL.Path, "/") {
					http.Redirect(w, r, c.AddrPath+r.URL.Path+"/", http.StatusTemporaryRedirect)
					return 0, nil
				}

				// Generate anti security token.
				c.GenerateToken()

				if !fi.IsDir {
					query := r.URL.Query()
					if val, ok := query["raw"]; ok && val[0] == "true" {
						r.URL.Path = strings.Replace(r.URL.Path, c.BaseURL, c.WebDavURL, 1)
						c.WebDavHandler.ServeHTTP(w, r)
						return 0, nil
					}

					if val, ok := query["download"]; ok && val[0] == "true" {
						w.Header().Set("Content-Disposition", "attachment; filename="+fi.Name)
						r.URL.Path = strings.Replace(r.URL.Path, c.BaseURL, c.WebDavURL, 1)
						c.WebDavHandler.ServeHTTP(w, r)
						return 0, nil
					}
				}

				code, err := fi.ServeAsHTML(w, r, c, user)
				if err != nil {
					return errors.PrintHTML(w, code, err)
				}
				return code, err
			}

			if r.Method == http.MethodPost {
				/* TODO: search commands. USE PROPFIND?
				// Search and git commands.
				if r.Header.Get("Search") == "true" {

				} */

				// VCS commands.
				if r.Header.Get("Command") != "" {
					if !user.AllowCommands {
						return http.StatusUnauthorized, nil
					}

					return command(w, r, c, user)
				}
			}

			return http.StatusNotImplemented, nil
		}
	}

	return f.Next.ServeHTTP(w, r)
}

// command handles the requests for VCS related commands: git, svn and mercurial
func command(w http.ResponseWriter, r *http.Request, c *config.Config, u *config.User) (int, error) {
	command := strings.Split(r.Header.Get("command"), " ")

	// Check if the command is allowed
	mayContinue := false

	for _, cmd := range u.Commands {
		if cmd == command[0] {
			mayContinue = true
		}
	}

	if !mayContinue {
		return http.StatusForbidden, nil
	}

	// Check if the program is talled is installed on the computer
	if _, err := exec.LookPath(command[0]); err != nil {
		return http.StatusNotImplemented, nil
	}

	path := strings.Replace(r.URL.Path, c.BaseURL, c.PathScope, 1)
	path = filepath.Clean(path)

	cmd := exec.Command(command[0], command[1:len(command)]...)
	cmd.Dir = path
	output, err := cmd.CombinedOutput()

	if err != nil {
		return http.StatusInternalServerError, err
	}

	page := &page.Page{Info: &page.Info{Data: string(output)}}
	return page.PrintAsJSON(w)
}
