package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

const (
	Algorithm         = "HS256"              // 首期唯一签名算法
	DefaultIssuer     = "cool-admin-go-next" // 默认签发者
	DefaultAudience   = "cool-admin"         // 默认受众
	DefaultAccessTTL  = 2 * time.Hour        // 默认 Access 有效期
	DefaultRefreshTTL = 15 * 24 * time.Hour  // 默认 Refresh 有效期
	DefaultClockSkew  = 30 * time.Second     // 默认时钟偏差
	minKeyBytes       = 32                   // HS256 最小密钥字节数
)

// JWT 配置
type JWTConfig struct {
	Issuer       string            `json:"issuer"`       // 签发者
	Audience     string            `json:"audience"`     // 受众
	CurrentKeyID string            `json:"currentKeyId"` // 当前签发密钥 ID
	AccessTTL    time.Duration     `json:"accessTTL"`    // Access 有效期
	RefreshTTL   time.Duration     `json:"refreshTTL"`   // Refresh 有效期
	ClockSkew    time.Duration     `json:"clockSkew"`    // 允许的时钟偏差
	Keys         map[string]string `json:"keys"`         // 验签密钥集合
}

// 返回 JWT 默认配置
func DefaultJWTConfig() JWTConfig {
	return JWTConfig{
		Issuer:     DefaultIssuer,
		Audience:   DefaultAudience,
		AccessTTL:  DefaultAccessTTL,
		RefreshTTL: DefaultRefreshTTL,
		ClockSkew:  DefaultClockSkew,
		Keys:       map[string]string{},
	}
}

// 校验 JWT 安全配置
func (config JWTConfig) Validate() error {
	if strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.Issuer) != config.Issuer {
		return exception.Core("JWT Issuer 无效")
	}
	if strings.TrimSpace(config.Audience) == "" || strings.TrimSpace(config.Audience) != config.Audience {
		return exception.Core("JWT Audience 无效")
	}
	if config.AccessTTL <= 0 || config.RefreshTTL <= 0 || config.AccessTTL >= config.RefreshTTL {
		return exception.Core("JWT TTL 必须为正数且 AccessTTL 小于 RefreshTTL")
	}
	if config.ClockSkew < 0 || config.ClockSkew >= config.AccessTTL {
		return exception.Core("JWT ClockSkew 必须非负且小于 AccessTTL")
	}
	if strings.TrimSpace(config.CurrentKeyID) == "" || strings.TrimSpace(config.CurrentKeyID) != config.CurrentKeyID {
		return exception.Core("JWT CurrentKeyID 无效")
	}
	if len(config.Keys) == 0 {
		return exception.Core("JWT Keys 不能为空")
	}
	if _, exists := config.Keys[config.CurrentKeyID]; !exists {
		return exception.Core("JWT CurrentKeyID 不存在")
	}
	for keyID, secret := range config.Keys {
		if strings.TrimSpace(keyID) == "" || strings.TrimSpace(keyID) != keyID {
			return exception.Core("JWT Key ID 无效")
		}
		if strings.TrimSpace(secret) != secret || len([]byte(secret)) < minKeyBytes {
			return exception.Core(fmt.Sprintf("JWT Key %q 至少需要 %d 个字节", keyID, minKeyBytes))
		}
	}

	return nil
}

// 管理端 JWT Claims
type AdminClaims struct {
	SessionID               string   `json:"sessionId"`    // Session ID
	Subject                 Kind     `json:"subject"`      // 身份种类
	IsRefresh               *bool    `json:"isRefresh"`    // Token 类型
	RoleIDs                 []uint64 `json:"roleIds"`      // 角色 ID
	Username                string   `json:"username"`     // 用户名
	UserID                  uint64   `json:"userId"`       // 用户 ID
	PasswordV               int      `json:"passwordV"`    // 密码版本
	AppID                   *uint64  `json:"id,omitempty"` // 禁止应用端字段
	jwtlib.RegisteredClaims          // JWT 注册 Claims
}

// 校验管理端私有 Claims
func (claims AdminClaims) Validate() error {
	if err := validateCommon(claims.SessionID, claims.Subject, claims.IsRefresh, claims.RegisteredClaims); err != nil {
		return err
	}
	if claims.Subject != AdminKind || claims.UserID == 0 || strings.TrimSpace(claims.Username) == "" ||
		claims.PasswordV <= 0 || claims.RoleIDs == nil || claims.AppID != nil {
		return errors.New("管理端 claims 无效")
	}
	for _, roleID := range claims.RoleIDs {
		if roleID == 0 {
			return errors.New("管理端 roleIds 无效")
		}
	}

	return nil
}

// 应用端 JWT Claims
type AppClaims struct {
	SessionID               string    `json:"sessionId"`           // Session ID
	Subject                 Kind      `json:"subject"`             // 身份种类
	ID                      uint64    `json:"id"`                  // 应用用户 ID
	IsRefresh               *bool     `json:"isRefresh"`           // Token 类型
	Username                *string   `json:"username,omitempty"`  // 禁止管理端字段
	RoleIDs                 *[]uint64 `json:"roleIds,omitempty"`   // 禁止管理端字段
	UserID                  *uint64   `json:"userId,omitempty"`    // 禁止管理端字段
	PasswordV               *int      `json:"passwordV,omitempty"` // 禁止管理端字段
	jwtlib.RegisteredClaims           // JWT 注册 Claims
}

// 校验应用端私有 Claims
func (claims AppClaims) Validate() error {
	if err := validateCommon(claims.SessionID, claims.Subject, claims.IsRefresh, claims.RegisteredClaims); err != nil {
		return err
	}
	if claims.Subject != AppKind || claims.ID == 0 || claims.Username != nil || claims.RoleIDs != nil ||
		claims.UserID != nil || claims.PasswordV != nil {
		return errors.New("应用端 claims 无效")
	}

	return nil
}

// 校验两类 Token 的共同必填 Claims
func validateCommon(
	sessionID string,
	subject Kind,
	isRefresh *bool,
	registered jwtlib.RegisteredClaims,
) error {
	if strings.TrimSpace(sessionID) == "" || subject == "" || isRefresh == nil {
		return errors.New("jwt 私有 claims 不完整")
	}
	if strings.TrimSpace(registered.ID) == "" || strings.TrimSpace(registered.Issuer) == "" ||
		len(registered.Audience) != 1 || registered.IssuedAt == nil || registered.NotBefore == nil || registered.ExpiresAt == nil {
		return errors.New("jwt 注册 claims 不完整")
	}
	if registered.IssuedAt.After(registered.NotBefore.Time) || !registered.ExpiresAt.After(registered.NotBefore.Time) {
		return errors.New("jwt 时间 claims 顺序无效")
	}

	return nil
}

// 令牌签发与验证服务
type JWT struct {
	config JWTConfig
	keys   map[string][]byte
	now    func() time.Time
	newJTI func() (string, error)
}

// 创建 JWT 服务
func NewJWT(config JWTConfig) (*JWT, error) {
	return newJWT(config, time.Now, randomID)
}

// 签发 Access 与 Refresh Token 对
func (service *JWT) IssuePair(subject TokenSubject) (Pair, error) {
	if err := checkIssueSubject(subject); err != nil {
		return Pair{}, err
	}
	accessJTI, err := service.newJTI()
	if err != nil {
		return Pair{}, exception.WrapCore(err, "生成 Access JTI 失败")
	}
	refreshJTI, err := service.newJTI()
	if err != nil {
		return Pair{}, exception.WrapCore(err, "生成 Refresh JTI 失败")
	}
	if accessJTI == refreshJTI {
		return Pair{}, exception.Core("JWT JTI 重复")
	}
	now := service.now().UTC()
	pair := Pair{
		AccessJTI:       accessJTI,
		RefreshJTI:      refreshJTI,
		AccessExpiresAt: now.Add(service.config.AccessTTL),
		ExpiresAt:       now.Add(service.config.RefreshTTL),
	}
	pair.AccessToken, err = service.sign(subject, accessJTI, false, now, pair.AccessExpiresAt)
	if err != nil {
		return Pair{}, err
	}
	pair.RefreshToken, err = service.sign(subject, refreshJTI, true, now, pair.ExpiresAt)
	if err != nil {
		return Pair{}, err
	}

	return pair, nil
}

// 解析并严格校验指定类型的 Token
func (service *JWT) Parse(encoded string, expectedRefresh bool) (Claims, error) {
	if strings.TrimSpace(encoded) == "" || strings.TrimSpace(encoded) != encoded {
		return Claims{}, ErrInvalidCredential
	}
	subject, err := service.readSubject(encoded)
	if err != nil {
		return Claims{}, ErrInvalidCredential
	}
	switch subject {
	case AdminKind:
		claims := &AdminClaims{}
		if err = service.parse(encoded, claims); err != nil {
			return Claims{}, ErrInvalidCredential
		}
		return adminTokenClaims(claims, expectedRefresh)
	case AppKind:
		claims := &AppClaims{}
		if err = service.parse(encoded, claims); err != nil {
			return Claims{}, ErrInvalidCredential
		}
		return appTokenClaims(claims, expectedRefresh)
	default:
		return Claims{}, ErrInvalidCredential
	}
}

// 创建可替换时钟和 JTI 源的 JWT 服务
func newJWT(config JWTConfig, now func() time.Time, newJTI func() (string, error)) (*JWT, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if now == nil || newJTI == nil {
		return nil, exception.Core("JWT 运行依赖不能为空")
	}
	keys := make(map[string][]byte, len(config.Keys))
	for keyID, secret := range config.Keys {
		keys[keyID] = []byte(secret)
	}
	config.Keys = nil

	return &JWT{config: config, keys: keys, now: now, newJTI: newJTI}, nil
}

// 签发单个 Token
func (service *JWT) sign(
	subject TokenSubject,
	jti string,
	isRefresh bool,
	issuedAt time.Time,
	expiresAt time.Time,
) (string, error) {
	registered := jwtlib.RegisteredClaims{
		Issuer:    service.config.Issuer,
		Audience:  jwtlib.ClaimStrings{service.config.Audience},
		ExpiresAt: jwtlib.NewNumericDate(expiresAt),
		NotBefore: jwtlib.NewNumericDate(issuedAt),
		IssuedAt:  jwtlib.NewNumericDate(issuedAt),
		ID:        jti,
	}
	refresh := isRefresh
	var claims jwtlib.Claims
	if subject.Subject == AdminKind {
		roleIDs := make([]uint64, len(subject.RoleIDs))
		copy(roleIDs, subject.RoleIDs)
		claims = &AdminClaims{
			SessionID:        subject.SessionID,
			Subject:          subject.Subject,
			IsRefresh:        &refresh,
			RoleIDs:          roleIDs,
			Username:         subject.Username,
			UserID:           subject.UserID,
			PasswordV:        subject.PasswordV,
			RegisteredClaims: registered,
		}
	} else {
		claims = &AppClaims{
			SessionID:        subject.SessionID,
			Subject:          subject.Subject,
			ID:               subject.UserID,
			IsRefresh:        &refresh,
			RegisteredClaims: registered,
		}
	}
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, claims)
	token.Header["kid"] = service.config.CurrentKeyID
	encoded, err := token.SignedString(service.keys[service.config.CurrentKeyID])
	if err != nil {
		return "", exception.WrapCore(err, "JWT 签名失败")
	}

	return encoded, nil
}

// 读取未验证的身份种类以选择严格 Claims 模型
func (service *JWT) readSubject(encoded string) (Kind, error) {
	claims := struct {
		Subject Kind `json:"subject"`
		jwtlib.RegisteredClaims
	}{}
	parser := jwtlib.NewParser(jwtlib.WithValidMethods([]string{Algorithm}), jwtlib.WithStrictDecoding())
	if _, _, err := parser.ParseUnverified(encoded, &claims); err != nil {
		return "", err
	}

	return claims.Subject, nil
}

// 验签并校验注册 Claims
func (service *JWT) parse(encoded string, claims jwtlib.Claims) error {
	parser := jwtlib.NewParser(
		jwtlib.WithValidMethods([]string{Algorithm}),
		jwtlib.WithIssuer(service.config.Issuer),
		jwtlib.WithAudience(service.config.Audience),
		jwtlib.WithExpirationRequired(),
		jwtlib.WithIssuedAt(),
		jwtlib.WithLeeway(service.config.ClockSkew),
		jwtlib.WithTimeFunc(service.now),
		jwtlib.WithStrictDecoding(),
	)
	token, err := parser.ParseWithClaims(encoded, claims, service.keyForToken)
	if err != nil || token == nil || !token.Valid {
		return ErrInvalidCredential
	}

	return nil
}

// 按 kid 返回固定 HS256 验签密钥
func (service *JWT) keyForToken(token *jwtlib.Token) (any, error) {
	if token == nil || token.Method != jwtlib.SigningMethodHS256 || token.Method.Alg() != Algorithm {
		return nil, ErrInvalidCredential
	}
	keyID, ok := token.Header["kid"].(string)
	if !ok || strings.TrimSpace(keyID) == "" {
		return nil, ErrInvalidCredential
	}
	key, exists := service.keys[keyID]
	if !exists {
		return nil, ErrInvalidCredential
	}

	return key, nil
}

// 转换管理端 Claims
func adminTokenClaims(claims *AdminClaims, expectedRefresh bool) (Claims, error) {
	if claims.IsRefresh == nil || *claims.IsRefresh != expectedRefresh {
		return Claims{}, ErrInvalidCredential
	}
	return tokenClaims(claims.SessionID, claims.Subject, claims.UserID, claims.Username, claims.RoleIDs, claims.PasswordV, *claims.IsRefresh, claims.RegisteredClaims)
}

// 转换应用端 Claims
func appTokenClaims(claims *AppClaims, expectedRefresh bool) (Claims, error) {
	if claims.IsRefresh == nil || *claims.IsRefresh != expectedRefresh {
		return Claims{}, ErrInvalidCredential
	}
	return tokenClaims(claims.SessionID, claims.Subject, claims.ID, "", nil, 0, *claims.IsRefresh, claims.RegisteredClaims)
}

// 转换共同 Claims
func tokenClaims(
	sessionID string,
	subject Kind,
	userID uint64,
	username string,
	roleIDs []uint64,
	passwordV int,
	isRefresh bool,
	registered jwtlib.RegisteredClaims,
) (Claims, error) {
	return Claims{
		TokenSubject: TokenSubject{
			SessionID: sessionID,
			Subject:   subject,
			UserID:    userID,
			Username:  username,
			RoleIDs:   append([]uint64(nil), roleIDs...),
			PasswordV: passwordV,
		},
		JTI:       registered.ID,
		IsRefresh: isRefresh,
		IssuedAt:  registered.IssuedAt.Time,
		NotBefore: registered.NotBefore.Time,
		ExpiresAt: registered.ExpiresAt.Time,
	}, nil
}

// 校验签发身份快照
func checkIssueSubject(subject TokenSubject) error {
	if strings.TrimSpace(subject.SessionID) == "" {
		return exception.Core("JWT 签发身份 Session ID 无效")
	}

	return validatePrincipal(Principal{
		Subject:   subject.Subject,
		UserID:    subject.UserID,
		Username:  subject.Username,
		RoleIDs:   subject.RoleIDs,
		PasswordV: subject.PasswordV,
	})
}

var (
	_ jwtlib.ClaimsValidator = (*AdminClaims)(nil)
	_ jwtlib.ClaimsValidator = (*AppClaims)(nil)
	_ Codec                  = (*JWT)(nil)
)
