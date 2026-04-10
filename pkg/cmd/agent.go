package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/runner"
	"github.com/spf13/cobra"
)

// newAgentCmd는 "piper agent" 커맨드를 반환한다.
// K8s Job entrypoint로 사용: piper agent exec --master=... --step=... -- <command>
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "K8s Pod 내부 step 실행 에이전트",
	}
	cmd.AddCommand(newAgentExecCmd())
	return cmd
}

type agentExecFlags struct {
	master    string
	token     string
	taskID    string
	runID     string
	stepName  string
	stepB64   string
	outputDir string
	inputDir  string
	// S3
	s3Endpoint  string
	s3AccessKey string
	s3SecretKey string
	s3Bucket    string
	s3UseSSL    bool
}

func newAgentExecCmd() *cobra.Command {
	var f agentExecFlags

	cmd := &cobra.Command{
		Use:   "exec [flags] -- <command...>",
		Short: "step을 실행하고 결과를 master에 보고한다",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentExec(cmd.Context(), f, args)
		},
	}

	cmd.Flags().StringVar(&f.master, "master", "", "piper server URL")
	cmd.Flags().StringVar(&f.token, "token", "", "인증 토큰")
	cmd.Flags().StringVar(&f.taskID, "task-id", "", "task ID")
	cmd.Flags().StringVar(&f.runID, "run-id", "", "run ID")
	cmd.Flags().StringVar(&f.stepName, "step-name", "", "step 이름")
	cmd.Flags().StringVar(&f.stepB64, "step", "", "base64 인코딩된 pipeline.Step JSON")
	cmd.Flags().StringVar(&f.outputDir, "output-dir", "/piper-outputs", "로컬 출력 디렉토리")
	cmd.Flags().StringVar(&f.inputDir, "input-dir", "/piper-inputs", "로컬 입력 디렉토리")
	cmd.Flags().StringVar(&f.s3Endpoint, "s3-endpoint", "", "S3 엔드포인트")
	cmd.Flags().StringVar(&f.s3AccessKey, "s3-access-key", "", "S3 액세스 키")
	cmd.Flags().StringVar(&f.s3SecretKey, "s3-secret-key", "", "S3 시크릿 키")
	cmd.Flags().StringVar(&f.s3Bucket, "s3-bucket", "", "S3 버킷")
	cmd.Flags().BoolVar(&f.s3UseSSL, "s3-use-ssl", false, "S3 SSL 사용")

	return cmd
}

func runAgentExec(ctx context.Context, f agentExecFlags, cmdArgs []string) error {
	var step pipeline.Step
	if f.stepB64 != "" {
		b, err := base64.StdEncoding.DecodeString(f.stepB64)
		if err != nil {
			return fmt.Errorf("decode step: %w", err)
		}
		if err := json.Unmarshal(b, &step); err != nil {
			return fmt.Errorf("unmarshal step: %w", err)
		}
	}

	// '--' 뒤 args가 있으면 step.Run.Command 오버라이드
	if len(cmdArgs) > 0 {
		step.Run.Command = cmdArgs
	}

	stepJSON, err := json.Marshal(step)
	if err != nil {
		return fmt.Errorf("marshal step: %w", err)
	}

	task := &proto.Task{
		ID:       f.taskID,
		RunID:    f.runID,
		StepName: f.stepName,
		Step:     stepJSON,
	}

	r, err := runner.New(runner.Config{
		MasterURL:   f.master,
		Token:       f.token,
		OutputDir:   f.outputDir,
		InputDir:    f.inputDir,
		S3Endpoint:  f.s3Endpoint,
		S3AccessKey: f.s3AccessKey,
		S3SecretKey: f.s3SecretKey,
		S3Bucket:    f.s3Bucket,
		S3UseSSL:    f.s3UseSSL,
	})
	if err != nil {
		return fmt.Errorf("runner init: %w", err)
	}

	r.Run(ctx, task)
	return nil
}
