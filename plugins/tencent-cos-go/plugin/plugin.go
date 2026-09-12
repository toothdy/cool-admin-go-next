package plugin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	cos "github.com/tencentyun/cos-go-sdk-v5"
	sts "github.com/tencentyun/qcloud-cos-sts-sdk/go"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/protocol"
	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/sdk"
)

const (
	defaultTTLSeconds  int64 = 1800
	defaultAllowPrefix       = "/*"
	maxPolicyBytes           = 1048576000
)

// 腾讯 COS 插件配置
type Config struct {
	AccessKeyID     string `json:"accessKeyId"`
	AccessKeySecret string `json:"accessKeySecret"`
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	PublicDomain    string `json:"publicDomain"`
	DurationSeconds int64  `json:"durationSeconds"`
	AllowPrefix     string `json:"allowPrefix"`
}

type uploadRequest struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	MIME     string `json:"mime"`
	Key      string `json:"key"`
}

type keyUploadRequest struct {
	Path string `json:"path"`
	Key  string `json:"key"`
}

type downloadRequest struct {
	URL      string `json:"url"`
	Filename string `json:"fileName"`
}

type modeResponse struct {
	Mode string `json:"mode"`
	Type string `json:"type"`
}

type uploadResponse struct {
	URL  string `json:"url"`
	Mode string `json:"mode"`
	Type string `json:"type"`
}

type httpRequest struct {
	Method   string              `json:"method"`
	URL      string              `json:"url"`
	Headers  map[string][]string `json:"headers,omitempty"`
	Body     []byte              `json:"body,omitempty"`
	BodyFile string              `json:"bodyFile,omitempty"`
}

type httpResponse struct {
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
}

type hostTransport struct {
	ctx  context.Context
	call func(context.Context, httpRequest) (httpResponse, error)
}

// 返回腾讯 COS 插件定义
func Plugin() sdk.Definition {
	return sdk.Define(
		sdk.RawMethod("mode", mode),
		sdk.RawMethod("getMode", mode),
		sdk.RawMethod("upload", upload),
		sdk.RawMethod("credentials", credentials),
		sdk.RawMethod("downAndUpload", downAndUpload),
		sdk.RawMethod("uploadWithKey", uploadWithKey),
		sdk.RawMethod("getConfig", getConfig),
		sdk.RawMethod("getMetaFileObj", unsupportedMeta),
	)
}

func mode(context.Context, json.RawMessage) (json.RawMessage, error) {
	return marshal(modeResponse{Mode: "cloud", Type: "cos"})
}

func getConfig(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	config, err := loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	config.AccessKeySecret = ""

	return marshal(config)
}

func credentials(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	return credentialsFor(ctx)
}

// 同时支持 Node 前端的直传凭证和宿主暂存文件的服务端上传
func upload(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var request uploadRequest
	if err := decode(input, &request); err != nil {
		return nil, err
	}
	if request.Path == "" {
		return credentialsFor(ctx)
	}
	if request.Size < 0 || request.Filename == "" {
		return nil, errors.New("上传文件参数无效")
	}
	if err := validateDataPath(request.Path); err != nil {
		return nil, err
	}
	config, err := loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	key, err := objectKey(config, request.Key, request.Filename, time.Now())
	if err != nil {
		return nil, err
	}
	if err = putObject(ctx, config, key, request.MIME, request.Path, nil); err != nil {
		return nil, fmt.Errorf("文件上传失败: %w", err)
	}

	return marshal(uploadResponse{URL: publicURL(config, key), Mode: "cloud", Type: "cos"})
}

func downAndUpload(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var request downloadRequest
	if err := decode(input, &request); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.URL) == "" {
		return nil, errors.New("下载地址不能为空")
	}
	config, err := loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	var data []byte
	if strings.HasPrefix(request.URL, "http://") || strings.HasPrefix(request.URL, "https://") {
		response, callErr := doHTTP(ctx, httpRequest{Method: "GET", URL: request.URL})
		if callErr != nil {
			return nil, fmt.Errorf("文件下载失败: %w", callErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("文件下载失败: HTTP %d", response.StatusCode)
		}
		data = response.Body
	} else {
		if err = validateDataPath(request.URL); err != nil {
			return nil, err
		}
		data, err = os.ReadFile(request.URL)
		if err != nil {
			return nil, fmt.Errorf("文件读取失败: %w", err)
		}
	}
	key, err := downloadedObjectKey(config, request.Filename, path.Base(request.URL), time.Now())
	if err != nil {
		return nil, err
	}
	if err = putObject(ctx, config, key, "application/octet-stream", "", data); err != nil {
		return nil, fmt.Errorf("文件上传失败: %w", err)
	}

	return marshal(publicURL(config, key))
}

func uploadWithKey(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var request keyUploadRequest
	if err := decode(input, &request); err != nil {
		return nil, err
	}
	if err := validateDataPath(request.Path); err != nil {
		return nil, err
	}
	config, err := loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateKey(request.Key); err != nil {
		return nil, errors.New("COS Key 不能为空且必须合法")
	}
	if err = putObject(ctx, config, request.Key, "application/octet-stream", request.Path, nil); err != nil {
		return nil, fmt.Errorf("文件上传失败: %w", err)
	}

	return marshal(publicURL(config, request.Key))
}

func unsupportedMeta(context.Context, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("WASM 插件不支持返回原始 COS 客户端")
}

func credentialsFor(ctx context.Context) (json.RawMessage, error) {
	config, err := loadConfig(ctx)
	if err != nil {
		return nil, err
	}

	return configCredentials(ctx, config)
}

func configCredentials(ctx context.Context, config Config) (json.RawMessage, error) {
	duration := config.DurationSeconds
	if duration <= 0 {
		duration = defaultTTLSeconds
	}
	allowPrefix := config.AllowPrefix
	if allowPrefix == "" {
		allowPrefix = defaultAllowPrefix
	}
	client := sts.NewClient(config.AccessKeyID, config.AccessKeySecret, &http.Client{
		Transport: hostTransport{ctx: ctx},
	})
	credential, err := client.GetCredential(&sts.CredentialOptions{
		DurationSeconds: duration,
		Region:          config.Region,
		Policy: &sts.CredentialPolicy{
			Version: "2.0",
			Statement: []sts.CredentialPolicyStatement{{
				Action:   uploadActions(),
				Effect:   "allow",
				Resource: []string{uploadResource(config, allowPrefix)},
			}},
		},
	})
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrorHostCallFailed, "STS 获取失败: "+err.Error(), err)
	}
	if credential == nil || credential.Credentials == nil ||
		credential.Credentials.TmpSecretID == "" || credential.Credentials.TmpSecretKey == "" ||
		credential.Credentials.SessionToken == "" || credential.ExpiredTime <= credential.StartTime {
		return nil, errors.New("STS 获取失败: 临时凭证为空")
	}

	return postCredentials(config, allowPrefix, credential)
}

func postCredentials(config Config, allowPrefix string, credential *sts.CredentialResult) (json.RawMessage, error) {
	now := time.Unix(int64(credential.StartTime), 0).UTC()
	expired := time.Unix(int64(credential.ExpiredTime), 0).UTC()
	keyPrefix := policyPrefix(allowPrefix)
	policy := postPolicy(config.Bucket, credential.Credentials.TmpSecretID, keyPrefix, now, expired)
	policyData, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	qKeyTime := fmt.Sprintf("%d;%d", now.Unix(), expired.Unix())
	signKey := hmacHex(credential.Credentials.TmpSecretKey, qKeyTime)
	policyDigest := sha1Hex(policyData)
	result := map[string]any{
		"expiredTime": credential.ExpiredTime,
		"expiration":  credential.Expiration,
		"credentials": map[string]string{
			"q-sign-algorithm":     "sha1",
			"q-ak":                 credential.Credentials.TmpSecretID,
			"q-key-time":           qKeyTime,
			"q-signature":          hmacHex(signKey, policyDigest),
			"policy":               base64.StdEncoding.EncodeToString(policyData),
			"x-cos-security-token": credential.Credentials.SessionToken,
		},
		"requestId": credential.RequestId,
		"startTime": credential.StartTime,
		"url":       config.PublicDomain,
	}

	return marshal(result)
}

func (transport hostTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil {
		var err error
		body, err = io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
	}
	headers := make(map[string][]string, len(request.Header))
	for name, values := range request.Header {
		if deniedForwardHeader(name) {
			continue
		}
		headers[name] = append([]string(nil), values...)
	}
	call := transport.call
	if call == nil {
		call = doHTTP
	}
	result, err := call(transport.ctx, httpRequest{
		Method:  request.Method,
		URL:     request.URL.String(),
		Headers: headers,
		Body:    body,
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: result.StatusCode,
		Status:     fmt.Sprintf("%d %s", result.StatusCode, http.StatusText(result.StatusCode)),
		Header:     http.Header(result.Headers),
		Body:       io.NopCloser(bytes.NewReader(result.Body)),
		Request:    request,
	}, nil
}

func deniedForwardHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "content-length", "host", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func uploadActions() []string {
	return []string{
		"name/cos:PostObject",
		"name/cos:PutObject",
		"name/cos:InitiateMultipartUpload",
		"name/cos:ListMultipartUploads",
		"name/cos:ListParts",
		"name/cos:UploadPart",
		"name/cos:CompleteMultipartUpload",
		"name/cos:AbortMultipartUpload",
	}
}

func uploadResource(config Config, allowPrefix string) string {
	separator := strings.LastIndexByte(config.Bucket, '-')
	appID := config.Bucket
	if separator >= 0 && separator+1 < len(config.Bucket) {
		appID = config.Bucket[separator+1:]
	}
	resource := "qcs::cos:" + config.Region + ":uid/" + appID + ":" + config.Bucket + "/"
	if allowPrefix == "*" || allowPrefix == "/*" {
		return resource + "*"
	}
	return resource + strings.TrimPrefix(allowPrefix, "/")
}

type postPolicyDocument struct {
	Expiration string `json:"expiration"`
	Conditions []any  `json:"conditions"`
}

func postPolicy(bucket, accessKeyID, prefix string, now, expired time.Time) postPolicyDocument {
	return postPolicyDocument{
		Expiration: expired.Format("2006-01-02T15:04:05.000Z"),
		Conditions: []any{
			map[string]string{"bucket": bucket},
			[]any{"starts-with", "$key", prefix},
			[]any{"content-length-range", 0, maxPolicyBytes},
			map[string]string{"q-sign-algorithm": "sha1"},
			map[string]string{"q-ak": accessKeyID},
			map[string]string{"q-sign-time": fmt.Sprintf("%d;%d", now.Unix(), expired.Unix())},
		},
	}
}

func putObject(ctx context.Context, config Config, key, contentType, bodyFile string, body []byte) error {
	objectURL, err := objectURL(config, key)
	if err != nil {
		return err
	}
	contentType = defaultContentType(contentType)
	authorization, err := cosAuthorization(config, objectURL, contentType)
	if err != nil {
		return err
	}
	request := httpRequest{
		Method: "PUT",
		URL:    objectURL,
		Headers: map[string][]string{
			"Authorization": {authorization},
			"Content-Type":  {contentType},
		},
		Body:     body,
		BodyFile: bodyFile,
	}
	response, err := doHTTP(ctx, request)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("COS HTTP %d", response.StatusCode)
	}

	return nil
}

func doHTTP(ctx context.Context, request httpRequest) (httpResponse, error) {
	payload, err := marshal(request)
	if err != nil {
		return httpResponse{}, err
	}
	responseData, err := sdk.HostCall(ctx, "http.do", payload)
	if err != nil {
		return httpResponse{}, err
	}
	var response httpResponse
	if err = json.Unmarshal(responseData, &response); err != nil {
		return httpResponse{}, fmt.Errorf("宿主 HTTP 响应无效: %w", err)
	}

	return response, nil
}

func loadConfig(ctx context.Context) (Config, error) {
	config, err := sdk.Config[Config](ctx)
	if err != nil {
		return Config{}, err
	}
	config.AccessKeyID = strings.TrimSpace(config.AccessKeyID)
	config.AccessKeySecret = strings.TrimSpace(config.AccessKeySecret)
	config.Bucket = strings.TrimSpace(config.Bucket)
	config.Region = strings.TrimSpace(config.Region)
	config.PublicDomain = strings.TrimRight(strings.TrimSpace(config.PublicDomain), "/")
	if config.AccessKeyID == "" || config.AccessKeySecret == "" || config.Bucket == "" || config.Region == "" || config.PublicDomain == "" {
		return Config{}, errors.New("请配置腾讯云 COS")
	}
	parsed, err := url.Parse(config.PublicDomain)
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Config{}, errors.New("COS 公共域名无效")
	}
	config.AllowPrefix, err = normPrefix(config.AllowPrefix)
	if err != nil {
		return Config{}, err
	}

	return config, nil
}

func objectKey(config Config, requested, filename string, now time.Time) (string, error) {
	if requested != "" {
		if err := validateKey(requested); err != nil {
			return "", err
		}
		prefix := policyPrefix(config.AllowPrefix)
		if prefix != "" && !strings.HasPrefix(requested, prefix) {
			return "", errors.New("COS Key 不符合 allowPrefix")
		}
		return requested, nil
	}
	filename = path.Base(strings.ReplaceAll(filename, "\\", "/"))
	if filename == "." || filename == ".." || filename == "" {
		return "", errors.New("文件名无效")
	}
	if err := validateKey(filename); err != nil {
		return "", err
	}
	randomName, err := randomHex(16)
	if err != nil {
		return "", err
	}
	ext := path.Ext(filename)
	return path.Join(uploadBase(config.AllowPrefix), now.Format("20060102"), randomName+ext), nil
}

func downloadedObjectKey(config Config, filename, fallback string, now time.Time) (string, error) {
	if filename == "" {
		return objectKey(config, "", fallback, now)
	}
	if err := validateKey(filename); err != nil {
		return "", err
	}
	key := path.Join(uploadBase(config.AllowPrefix), now.Format("20060102"), filename)
	if err := validateKey(key); err != nil {
		return "", err
	}

	return key, nil
}

func uploadBase(allowPrefix string) string {
	base := strings.TrimSuffix(allowPrefix, "/*")
	base = strings.TrimSuffix(base, "*")
	base = strings.TrimRight(base, "/")
	if base == "" {
		return "app/base"
	}

	return base
}

func publicURL(config Config, key string) string {
	return strings.TrimRight(config.PublicDomain, "/") + "/" + key
}

func objectURL(config Config, key string) (string, error) {
	parts := strings.Split(key, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return "https://" + config.Bucket + ".cos." + config.Region + ".myqcloud.com/" + strings.Join(parts, "/"), nil
}

func cosAuthorization(config Config, rawURL, contentType string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequest(http.MethodPut, rawURL, http.NoBody)
	if err != nil {
		return "", err
	}
	request.Host = parsed.Host
	request.Header.Set("Content-Type", contentType)
	cos.AddAuthorizationHeader(
		config.AccessKeyID,
		config.AccessKeySecret,
		"",
		request,
		cos.NewAuthTime(time.Hour),
	)

	return request.Header.Get("Authorization"), nil
}

func policyPrefix(value string) string {
	if value == "" || value == "*" || value == "/*" {
		return ""
	}
	value = strings.TrimSuffix(value, "/*")
	return strings.TrimRight(value, "/") + "/"
}

func normPrefix(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "*" || value == "/*" {
		return defaultAllowPrefix, nil
	}
	if strings.HasPrefix(value, "/") || !strings.HasSuffix(value, "/*") {
		return "", errors.New("allowPrefix 必须是 /* 或安全的相对目录/*")
	}
	base := strings.TrimSuffix(value, "/*")
	if strings.Contains(base, "*") || validateKey(base) != nil {
		return "", errors.New("allowPrefix 必须是 /* 或安全的相对目录/*")
	}

	return base + "/*", nil
}

func validateDataPath(value string) error {
	if !strings.HasPrefix(value, "/data/") || strings.Contains(value, "\\") || strings.Contains(value, "\x00") {
		return errors.New("文件路径必须位于插件 /data 目录")
	}
	clean := path.Clean(value)
	if clean != value || strings.Contains(clean, "/../") || clean == "/data" {
		return errors.New("文件路径无效")
	}
	return nil
}

func validateKey(value string) error {
	if value == "" || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") {
		return errors.New("COS Key 无效")
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return errors.New("COS Key 无效")
	}
	return nil
}

func defaultContentType(value string) string {
	if strings.TrimSpace(value) == "" {
		return "application/octet-stream"
	}
	return value
}

func randomHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func hmacHex(key, value string) string {
	hash := hmac.New(sha1.New, []byte(key))
	_, _ = hash.Write([]byte(value))
	return hex.EncodeToString(hash.Sum(nil))
}

func sha1Hex(value []byte) string {
	digest := sha1.Sum(value)
	return hex.EncodeToString(digest[:])
}

func decode(input json.RawMessage, target any) error {
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	decoder := json.NewDecoder(strings.NewReader(string(input)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("插件参数无效: %w", err)
	}

	return nil
}

func marshal(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("插件响应无效: %w", err)
	}

	return data, nil
}
