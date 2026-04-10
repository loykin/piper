// piper-agent는 K8s Pod 내부에서 실행되는 step 실행 에이전트다.
//
// 역할:
//  1. S3에서 입력 아티팩트 다운로드
//  2. step 커맨드 실행 (stdout/stderr 캡처)
//  3. 로그를 piper server로 배치 전송
//  4. S3에 출력 아티팩트 업로드
//  5. piper server에 done/failed 보고
//
// K8s Job에서 entrypoint로 사용:
//
//	/piper-tools/piper-agent exec \
//	  --master=http://piper:8080 \
//	  --task-id=run-123:prep \
//	  --run-id=run-123 \
//	  --step-name=prep \
//	  --step=<base64 JSON> \
//	  -- python train.py
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
	"github.com/piper/piper/pkg/runner"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "piper-agent",
		Short: "piper agent — K8s Pod 내부 step 실행기",
	}
	root.AddCommand(newExecCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

type execFlags struct {
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

func newExecCmd() *cobra.Command {
	var f execFlags

	cmd := &cobra.Command{
		Use:   "exec [flags] -- <command...>",
		Short: "step을 실행하고 결과를 master에 보고한다",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd.Context(), f, args)
		},
	}

	cmd.Flags().StringVar(&f.master, "master", "", "piper server URL (필수)")
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

func runExec(ctx context.Context, f execFlags, cmdArgs []string) error {
	// step JSON 디코딩
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

	// '--' 뒤 args로 command 오버라이드 (step.Run.Command보다 우선)
	if len(cmdArgs) > 0 {
		step.Run.Command = cmdArgs
	}

	// step JSON을 다시 직렬화해서 proto.Task에 넣기
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
