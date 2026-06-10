package commands

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/piper/piper/pkg/notebook"
	"github.com/spf13/cobra"
)

func newInternalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "internal",
		Short:  "Internal piper subcommands (not for direct user use)",
		Hidden: true,
	}
	cmd.AddCommand(newInternalVolumeBrowserCmd())
	return cmd
}

func newInternalVolumeBrowserCmd() *cobra.Command {
	var root, addr, tokenFile string

	cmd := &cobra.Command{
		Use:   "volume-browser",
		Short: "Read-only HTTP file browser for a mounted volume",
		RunE: func(cmd *cobra.Command, args []string) error {
			token := ""
			if tokenFile != "" {
				data, err := os.ReadFile(tokenFile)
				if err != nil {
					return err
				}
				token = strings.TrimSpace(string(data))
			} else if envToken := os.Getenv("PIPER_BROWSER_TOKEN"); envToken != "" {
				token = envToken
			}

			mux := http.NewServeMux()
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			mux.HandleFunc("/files", newFilesHandler(root, token))

			slog.Info("volume-browser listening", "addr", addr, "root", root)
			return http.ListenAndServe(addr, mux)
		},
	}

	cmd.Flags().StringVar(&root, "root", "/data", "volume mount root directory")
	cmd.Flags().StringVar(&addr, "addr", ":8080", "listen address")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "path to bearer token file for authentication")

	return cmd
}

func newFilesHandler(root, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		rawPath := r.URL.Query().Get("path")
		if strings.Contains(rawPath, "..") {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		subPath := path.Clean("/" + rawPath)

		walkRoot := root
		if subPath != "/" {
			walkRoot = root + subPath
		}

		var extList []string
		if extParam := r.URL.Query().Get("ext"); extParam != "" {
			for _, e := range strings.Split(extParam, ",") {
				if e = strings.TrimSpace(e); e != "" {
					extList = append(extList, e)
				}
			}
		}

		maxFiles := 500
		if mfStr := r.URL.Query().Get("max_files"); mfStr != "" {
			if n, err := strconv.Atoi(mfStr); err == nil && n > 0 {
				maxFiles = n
			}
		}

		files, truncated := notebook.WalkFiles(walkRoot, extList, maxFiles)
		if files == nil {
			files = []string{}
		}

		// Prefix results with subPath so they are volume-root-relative.
		if subPath != "/" {
			prefix := strings.TrimPrefix(subPath, "/")
			for i, f := range files {
				files[i] = prefix + "/" + f
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if truncated {
			w.Header().Set("X-Piper-Files-Truncated", "true")
		}
		_ = json.NewEncoder(w).Encode(files)
	}
}
