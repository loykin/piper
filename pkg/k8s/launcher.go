// Package k8s는 piper task를 K8s Job으로 실행하는 Dispatcher 구현체를 제공한다.
//
// 동작 방식 (agent injection 패턴):
//  1. initContainer가 piper/agent 이미지에서 /piper-agent 바이너리를 emptyDir에 복사
//  2. step 컨테이너의 entrypoint를 /piper-tools/piper-agent exec ... -- <원래 커맨드>로 교체
//  3. piper-agent가 S3에서 입력 아티팩트 다운로드 → 커맨드 실행 → S3에 출력 업로드 → master에 완료 보고
//
// 사용자 이미지 수정 없이 K8s-native 실행 가능.
package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

// Config는 Launcher 설정.
// piper.K8sConfig와 1:1 대응.
type Config struct {
	// AgentImage: piper-agent 바이너리가 담긴 이미지 (initContainer용)
	AgentImage string

	// Namespace: Job을 생성할 K8s 네임스페이스
	Namespace string

	// InCluster: true이면 in-cluster config 사용
	InCluster bool

	// Kubeconfig: out-of-cluster 실행 시 kubeconfig 파일 경로 (비어 있으면 KUBECONFIG 또는 ~/.kube/config)
	Kubeconfig string

	// MasterURL: Pod에서 접근 가능한 piper server URL
	MasterURL string

	// Token: piper server 인증 토큰
	Token string

	// S3 아티팩트 공유 설정
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool

	// DefaultImage: step에 image가 없을 때 사용할 기본 컨테이너 이미지
	DefaultImage string

	// TTLAfterFinished: Job 완료 후 자동 삭제 시간(초). nil이면 자동 삭제 안 함.
	TTLAfterFinished *int32
}

// Launcher는 proto.Dispatcher를 구현한다.
// task가 ready 상태가 되면 queue가 Dispatch를 호출한다.
type Launcher struct {
	cfg       Config
	clientset *kubernetes.Clientset
}

// New는 Launcher를 생성한다.
// InCluster=true이면 in-cluster config를 사용하고,
// 그렇지 않으면 Kubeconfig 경로(또는 기본 위치)의 kubeconfig를 사용한다.
func New(cfg Config) (*Launcher, error) {
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	if cfg.AgentImage == "" {
		cfg.AgentImage = "piper/piper:latest"
	}

	var restCfg *rest.Config
	var err error
	if cfg.InCluster {
		restCfg, err = rest.InClusterConfig()
	} else {
		kubeconfig := cfg.Kubeconfig
		if kubeconfig == "" {
			kubeconfig = os.Getenv("KUBECONFIG")
		}
		if kubeconfig == "" {
			home, _ := os.UserHomeDir()
			kubeconfig = home + "/.kube/config"
		}
		restCfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s clientset: %w", err)
	}

	return &Launcher{cfg: cfg, clientset: clientset}, nil
}

// Dispatch는 task에 대응하는 K8s Job을 생성한다.
// Job 완료를 기다리지 않는다 — Job 내부의 piper-agent가 master로 결과를 보고한다.
func (l *Launcher) Dispatch(ctx context.Context, task *proto.Task) error {
	var step pipeline.Step
	if err := json.Unmarshal(task.Step, &step); err != nil {
		return fmt.Errorf("unmarshal step: %w", err)
	}

	var pl pipeline.Pipeline
	if err := json.Unmarshal(task.Pipeline, &pl); err != nil {
		return fmt.Errorf("unmarshal pipeline: %w", err)
	}

	// 컨테이너 이미지 결정: step > pipeline defaults > launcher default
	image := step.Run.Image
	if image == "" {
		image = pl.Spec.Defaults.Image
	}
	if image == "" {
		image = l.cfg.DefaultImage
	}
	if image == "" {
		return fmt.Errorf("step %q: no container image configured (set step.run.image, spec.defaults.image, or k8s.default_image)", step.Name)
	}

	// step JSON을 base64로 인코딩해서 agent에 전달
	stepJSON, err := json.Marshal(step)
	if err != nil {
		return err
	}
	stepB64 := base64.StdEncoding.EncodeToString(stepJSON)

	agentArgs := l.buildAgentArgs(task, stepB64)
	if len(step.Run.Command) > 0 {
		agentArgs = append(agentArgs, "--")
		agentArgs = append(agentArgs, step.Run.Command...)
	}

	job := l.buildJob(task, image, agentArgs)

	_, err = l.clientset.BatchV1().Jobs(l.cfg.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create job %s: %w", job.Name, err)
	}
	return nil
}

func (l *Launcher) buildAgentArgs(task *proto.Task, stepB64 string) []string {
	args := []string{
		"agent", "exec",
		"--master=" + l.cfg.MasterURL,
		"--task-id=" + task.ID,
		"--run-id=" + task.RunID,
		"--step-name=" + task.StepName,
		"--step=" + stepB64,
		"--output-dir=/piper-outputs",
		"--input-dir=/piper-inputs",
	}
	if l.cfg.Token != "" {
		args = append(args, "--token="+l.cfg.Token)
	}
	if l.cfg.S3Endpoint != "" {
		args = append(args,
			"--s3-endpoint="+l.cfg.S3Endpoint,
			"--s3-access-key="+l.cfg.S3AccessKey,
			"--s3-secret-key="+l.cfg.S3SecretKey,
			"--s3-bucket="+l.cfg.S3Bucket,
		)
		if l.cfg.S3UseSSL {
			args = append(args, "--s3-use-ssl")
		}
	}
	return args
}

func (l *Launcher) buildJob(task *proto.Task, image string, agentArgs []string) *batchv1.Job {
	backoffLimit := int32(0) // piper queue가 재시도 관리
	var ttl *int32
	if l.cfg.TTLAfterFinished != nil && *l.cfg.TTLAfterFinished > 0 {
		ttl = l.cfg.TTLAfterFinished
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName(task),
			Namespace: l.cfg.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "piper",
				"piper/run-id":                 sanitizeLabel(task.RunID),
				"piper/step-name":              sanitizeLabel(task.StepName),
			},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: ttl,
			BackoffLimit:            &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					// initContainer: piper-agent 바이너리를 emptyDir에 복사
					InitContainers: []corev1.Container{
						{
							Name:            "agent-init",
							Image:           l.cfg.AgentImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							// piper/agent 이미지 내 /piper-agent 바이너리를 공유 볼륨에 복사
							Command: []string{"cp", "/piper", "/piper-tools/piper"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "piper-tools", MountPath: "/piper-tools"},
							},
						},
					},
					// step 컨테이너: 원래 이미지에서 piper-agent를 entrypoint로 실행
					Containers: []corev1.Container{
						{
							Name:            "step",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/piper-tools/piper"},
							Args:            agentArgs,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "piper-tools", MountPath: "/piper-tools"},
								{Name: "piper-outputs", MountPath: "/piper-outputs"},
								{Name: "piper-inputs", MountPath: "/piper-inputs"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "piper-tools",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "piper-outputs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "piper-inputs",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}
}

// jobName은 K8s Job 이름을 생성한다.
// 형식: piper-{runID}-{stepName}, 63자 이하로 truncate.
func jobName(task *proto.Task) string {
	raw := "piper-" + task.RunID + "-" + task.StepName
	return sanitizeName(raw)
}

// sanitizeName은 K8s 리소스 이름 규칙에 맞게 문자열을 정규화한다.
// [a-z0-9-], 63자 이하, 시작/끝은 알파벳 또는 숫자.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteRune(c)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// sanitizeLabel은 K8s label value 규칙에 맞게 정규화한다.
// [a-zA-Z0-9._-], 63자 이하.
func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteRune(c)
		default:
			b.WriteRune('-')
		}
	}
	v := b.String()
	if len(v) > 63 {
		v = v[:63]
	}
	return v
}
