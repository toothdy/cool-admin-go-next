package auth

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
)

const adminPathPrefix = "/admin/" // 参与菜单权限校验的后台路由前缀

const commPathSegment = "comm" // 只校验登录的通用接口路径段

const adminDictDataPath = "/admin/dict/info/data" // 只校验登录的后台字典数据路径

// 从后台路由路径推导菜单权限标识
func DerivePermission(fullPath string, ignoreToken bool) (string, error) {
	if ignoreToken || !strings.HasPrefix(fullPath, adminPathPrefix) {
		return "", nil
	}
	if fullPath == adminDictDataPath {
		return "", nil
	}
	remainder := strings.Trim(strings.TrimPrefix(fullPath, adminPathPrefix), "/")
	if remainder == "" {
		return "", exception.Core(fmt.Sprintf("后台路由 %q 无法推导权限标识", fullPath))
	}

	segments := strings.Split(remainder, "/")
	for index, segment := range segments {
		if segment == commPathSegment && index != len(segments)-1 {
			return "", nil
		}
	}
	for _, segment := range segments {
		if !validSegment(segment) {
			return "", exception.Core(fmt.Sprintf("后台路由 %q 的路径段 %q 不是合法权限标识", fullPath, segment))
		}
	}

	return strings.Join(segments, ":"), nil
}

// 权限标识段允许的字符形状
func validSegment(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character == '_':
		case character >= '0' && character <= '9':
			if index == 0 {
				return false
			}
		default:
			return false
		}
	}

	return true
}

// 构造规范化 HTTP 权限资源
func HTTPResource(method, requestPath string) (string, error) {
	normalizedMethod := strings.ToUpper(strings.TrimSpace(method))
	switch normalizedMethod {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace:
	default:
		return "", exception.Core("鉴权 HTTP Method 无效")
	}
	if !validResourcePath(requestPath) || path.Clean(requestPath) != requestPath {
		return "", exception.Core("鉴权 HTTP Path 未规范化")
	}

	return normalizedMethod + " " + requestPath, nil
}

// 构造规范化 gRPC 权限资源
func GRPCResource(fullMethod string) (string, error) {
	if !validResourcePath(fullMethod) || path.Clean(fullMethod) != fullMethod {
		return "", exception.Core("鉴权 gRPC FullMethod 未规范化")
	}
	parts := strings.Split(strings.TrimPrefix(fullMethod, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", exception.Core("鉴权 gRPC FullMethod 无效")
	}

	return fullMethod, nil
}

// 校验协议资源路径
func validResourcePath(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}

	return true
}

// 协议无关的授权请求
type Authorization struct {
	Subject    Kind   // 身份种类
	SubjectID  uint64 // 身份 ID
	Permission string // 权限标识
	Resource   string // 协议资源
}

// 业务授权器
type Authorizer interface {
	Authorize(context.Context, Authorization) (bool, error)
}

// 执行统一授权判断
func Authorize(ctx context.Context, authorizer Authorizer, permission, resource string) error {
	if authorizer == nil {
		return exception.Core("授权器不能为空")
	}
	if strings.TrimSpace(permission) == "" {
		return exception.Core("授权权限标识不能为空")
	}
	if strings.TrimSpace(resource) == "" {
		return exception.Core("授权资源不能为空")
	}

	identity, exists := identityFromContext(ctx)
	if !exists {
		return unauthErr()
	}
	authorization, err := toAuthz(identity, permission, resource)
	if err != nil {
		return err
	}

	allowed, err := authorizer.Authorize(ctx, authorization)
	if err != nil {
		return exception.WrapCore(err, "执行权限校验失败")
	}
	if !allowed {
		return exception.Comm("权限不足", http.StatusForbidden)
	}

	return nil
}

// 从已验证身份构造授权请求
func toAuthz(identity verifiedIdentity, permission, resource string) (Authorization, error) {
	authorization := Authorization{
		Subject:    identity.subject,
		Permission: permission,
		Resource:   resource,
	}

	switch identity.subject {
	case AdminKind:
		authorization.SubjectID = identity.admin.UserID
	case AppKind:
		authorization.SubjectID = identity.app.ID
	default:
		return Authorization{}, exception.Core("授权身份种类无效")
	}
	if authorization.SubjectID == 0 {
		return Authorization{}, exception.Core("授权身份 ID 不能为空")
	}

	return authorization, nil
}

// 按 HTTP 路由元数据执行认证和授权
func (service *Service) AuthenticateHTTP(
	ctx context.Context,
	authorization string,
	method string,
	requestPath string,
	permission string,
	ignoreToken bool,
) (context.Context, error) {
	return service.authProtocol(ctx, authorization, permission, ignoreToken, func() (string, error) {
		return HTTPResource(method, requestPath)
	})
}

// 按 gRPC 方法元数据执行认证和授权
func (service *Service) AuthenticateGRPC(
	ctx context.Context,
	authorization string,
	fullMethod string,
	permission string,
	ignoreToken bool,
) (context.Context, error) {
	return service.authProtocol(ctx, authorization, permission, ignoreToken, func() (string, error) {
		return GRPCResource(fullMethod)
	})
}

// 按协议元数据构造规则并执行认证
func (service *Service) authProtocol(
	ctx context.Context,
	authorization string,
	permission string,
	ignoreToken bool,
	resource func() (string, error),
) (context.Context, error) {
	if ignoreToken {
		return ctx, nil
	}
	if authorization == "" {
		return ctx, credentialErr()
	}
	rule := Rule{Permission: permission}
	if strings.TrimSpace(permission) != "" {
		built, err := resource()
		if err != nil {
			return ctx, err
		}
		rule.Resource = built
	}

	return service.Authenticate(ctx, authorization, rule)
}

// 授权关系变更的统一入口：先锁目标行再写入，必要时撤销 Session
type Boundary struct {
	runtime  *db.Runtime
	sessions Store
}

// 授权变更边界
func NewBoundary(runtime *db.Runtime, sessions Store) (*Boundary, error) {
	if runtime.Runner() == nil || sessions == nil {
		return nil, exception.Core("授权变更边界依赖无效")
	}

	return &Boundary{runtime: runtime, sessions: sessions}, nil
}

// 授权变更使用的行锁与存在性校验
func (boundary *Boundary) LockTable(ctx context.Context, table string, ids []uint64, message string) error {
	ids = NormalizeIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	locked, err := boundary.runtime.LockRows(ctx, table, ids)
	if err != nil {
		return exception.WrapCore(err, message)
	}
	if !slices.Equal(ids, locked) {
		return exception.Validate(message + ": 目标记录不存在")
	}

	return nil
}

// 授权变更后让目标用户的旧 Token 失效
func (boundary *Boundary) LockUsersAndRevoke(ctx context.Context, table string, userIDs []uint64, kind Kind, message string) error {
	userIDs = NormalizeIDs(userIDs)
	if len(userIDs) == 0 {
		return nil
	}
	if err := boundary.LockTable(ctx, table, userIDs, message); err != nil {
		return err
	}
	if err := boundary.sessions.RevokeUsers(ctx, kind, userIDs); err != nil {
		return exception.WrapCore(err, "撤销用户 Session 失败")
	}

	return nil
}

// 调用方已加锁时跳过重复加锁的 Session 撤销
func (boundary *Boundary) RevokeUsers(ctx context.Context, kind Kind, userIDs []uint64) error {
	userIDs = NormalizeIDs(userIDs)
	if len(userIDs) == 0 {
		return nil
	}
	if err := boundary.sessions.RevokeUsers(ctx, kind, userIDs); err != nil {
		return exception.WrapCore(err, "撤销用户 Session 失败")
	}

	return nil
}

// 授权变更提交前的并发一致性校验
func ValidateSnapshot(before, after []uint64, message string) error {
	if !slices.Equal(NormalizeIDs(before), NormalizeIDs(after)) {
		return exception.Comm(message)
	}

	return nil
}

// 为交叉加锁准备一致的 ID 顺序
func NormalizeIDs(ids []uint64) []uint64 {
	result := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if id != 0 {
			result = append(result, id)
		}
	}
	slices.Sort(result)

	return slices.Compact(result)
}
