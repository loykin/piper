package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/piper/piper/pkg/storage"
	"github.com/spf13/cobra"
)

func newInternalArtifactDownloadCmd() *cobra.Command {
	var storageURL, storageToken, prefix, destination string
	cmd := &cobra.Command{
		Use:   "artifact-download",
		Short: "Download an artifact prefix into a local directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if storageURL == "" {
				storageURL = os.Getenv("PIPER_STORAGE_URL")
			}
			if storageToken == "" {
				storageToken = os.Getenv("PIPER_STORAGE_TOKEN")
			}
			if prefix == "" {
				prefix = os.Getenv("PIPER_ARTIFACT_KEY")
			}
			if destination == "" {
				destination = os.Getenv("PIPER_ARTIFACT_DEST")
			}
			if strings.TrimSpace(storageURL) == "" || strings.TrimSpace(prefix) == "" || strings.TrimSpace(destination) == "" {
				return fmt.Errorf("storage URL, artifact key, and destination are required")
			}
			store, err := storage.Open(storageURL, storageToken)
			if err != nil {
				return err
			}
			return storage.DownloadDir(context.Background(), store, prefix, destination)
		},
	}
	cmd.Flags().StringVar(&storageURL, "storage-url", "", "artifact storage URL")
	cmd.Flags().StringVar(&storageToken, "storage-token", "", "artifact storage bearer token")
	cmd.Flags().StringVar(&prefix, "prefix", "", "artifact key prefix")
	cmd.Flags().StringVar(&destination, "destination", "", "download destination")
	return cmd
}
