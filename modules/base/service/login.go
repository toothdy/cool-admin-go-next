package service

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/auth"
	"github.com/toothdy/cool-admin-go-next/cool-next/auth/bcrypt"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/cool-next/db"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/driver"
	"github.com/toothdy/cool-admin-go-next/cool-next/risk"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
	"github.com/toothdy/cool-admin-go-next/modules/base/entity"
)

type loginUserRow struct {
	ID        uint64 `orm:"id"`
	Username  string `orm:"username"`
	Password  string `orm:"password"`
	PasswordV int32  `orm:"passwordV"`
	Status    int32  `orm:"status"`
}

type loginUserWriteLock struct {
	ID uint64 `orm:"id"`
}

type loginPasswordUpdate struct {
	Password string `orm:"password"`
}

var errRolesMissing = errors.New("登录用户未配置角色")

// 后台登录、刷新和退出能力
type LoginService struct {
	runtime    *db.Runtime
	user       *gnservice.Base[entity.User, uint64]
	captcha    *CaptchaService
	risk       *risk.Service
	password   *bcrypt.Verifier
	auth       *auth.Service
	permission *PermissionService
	sessions   auth.Store
}

// 后台登录服务
func NewLogin(
	runtime *db.Runtime,
	user *gnservice.Base[entity.User, uint64],
	captcha *CaptchaService,
	riskService *risk.Service,
	password *bcrypt.Verifier,
	authService *auth.Service,
	permission *PermissionService,
	sessions auth.Store,
) *LoginService {

	return &LoginService{
		runtime: runtime, user: user, captcha: captcha, risk: riskService, password: password, auth: authService,
		permission: permission, sessions: sessions,
	}
}

// 校验验证码和账号后创建后台 Session
func (service *LoginService) Login(ctx context.Context, clientIP string, request dto.LoginReq) (dto.TokenResult, error) {
	if err := service.risk.AllowLogin(ctx, clientIP, request.Username); err != nil {
		return dto.TokenResult{}, err
	}
	verified, err := service.captcha.Verify(ctx, request.CaptchaID, request.VerifyCode)
	if err != nil {
		return dto.TokenResult{}, err
	}
	if !verified {
		return dto.TokenResult{}, exception.Comm("验证码不正确")
	}
	candidate, err := service.userByUsername(ctx, request.Username)
	if err != nil {
		return dto.TokenResult{}, err
	}
	if candidate == nil || candidate.Status != 1 {
		if err = service.risk.RecordLoginFailure(ctx, request.Username); err != nil {
			return dto.TokenResult{}, err
		}
		return dto.TokenResult{}, credentialErr()
	}
	result, err := service.password.Verify(request.Password, candidate.Password)
	if err != nil {
		return dto.TokenResult{}, err
	}
	if !result.Valid {
		if err = service.risk.RecordLoginFailure(ctx, request.Username); err != nil {
			return dto.TokenResult{}, err
		}
		return dto.TokenResult{}, credentialErr()
	}
	if err = service.risk.ClearLoginFailure(ctx, request.Username); err != nil {
		return dto.TokenResult{}, err
	}
	var rehashedPassword string
	if result.NeedsRehash {
		rehashedPassword, err = service.password.Hash(request.Password)
		if err != nil {
			return dto.TokenResult{}, err
		}
	}

	var pair auth.Pair
	err = service.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		current, lockErr := service.lockedUser(txCtx, candidate.ID)
		if lockErr != nil {
			return lockErr
		}
		if current == nil || current.Username != candidate.Username || current.Status != 1 ||
			current.Password != candidate.Password || current.PasswordV != candidate.PasswordV {
			return credentialErr()
		}
		principal, principalErr := service.adminPrincipal(txCtx, current)
		if errors.Is(principalErr, errRolesMissing) {
			return exception.Comm("该用户未设置任何角色，无法登录~")
		}
		if principalErr != nil {
			return principalErr
		}
		if rehashedPassword != "" {
			if rehashErr := service.updatePasswordHash(txCtx, current.ID, rehashedPassword); rehashErr != nil {
				return rehashErr
			}
		}
		pair, principalErr = service.auth.Create(txCtx, principal)

		return principalErr
	})
	if err != nil {
		return dto.TokenResult{}, err
	}

	return service.tokenResult(pair), nil
}

// 锁定用户并按权威状态重建身份后轮换 Token
func (service *LoginService) Refresh(ctx context.Context, request dto.RefreshReq) (dto.TokenResult, error) {
	var pair auth.Pair
	err := service.runtime.Runner().Within(ctx, func(txCtx context.Context) error {
		var refreshErr error
		pair, refreshErr = service.auth.RefreshWith(
			txCtx,
			request.RefreshToken,
			func(resolveCtx context.Context, current auth.Principal) (auth.Principal, error) {
				if current.Subject != auth.AdminKind || current.UserID == 0 {
					return auth.Principal{}, loginExpiredError()
				}
				user, lockErr := service.lockedUser(resolveCtx, current.UserID)
				if lockErr != nil {
					return auth.Principal{}, lockErr
				}
				if user == nil || user.Status != 1 {
					return auth.Principal{}, loginExpiredError()
				}
				principal, principalErr := service.adminPrincipal(resolveCtx, user)
				if errors.Is(principalErr, errRolesMissing) {
					return auth.Principal{}, loginExpiredError()
				}
				if principalErr != nil {
					return auth.Principal{}, principalErr
				}

				return principal, nil
			},
		)

		return refreshErr
	})
	if err != nil {
		return dto.TokenResult{}, err
	}

	return service.tokenResult(pair), nil
}

// 撤销当前已认证的后台 Session
func (service *LoginService) Logout(ctx context.Context) error {
	if _, err := auth.Admin(ctx); err != nil {
		return err
	}
	session, exists := auth.Session(ctx)
	if !exists || session.Subject != auth.AdminKind || session.ID == "" {
		return loginExpiredError()
	}
	if err := service.sessions.Revoke(ctx, session.ID); err != nil {
		return exception.WrapCore(err, "撤销登录 Session 失败")
	}

	return nil
}

func (service *LoginService) userByUsername(ctx context.Context, username string) (*loginUserRow, error) {
	model, err := service.user.Model(ctx)
	if err != nil {
		return nil, err
	}
	var user *loginUserRow
	err = model.
		Fields("id", "username", "password", "passwordV", "status").
		Where("username", username).
		Scan(&user)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, exception.WrapCore(err, "查询登录用户失败")
	}

	return user, nil
}

func (service *LoginService) lockedUser(ctx context.Context, userID uint64) (*loginUserRow, error) {
	if _, err := service.user.Tx(ctx); err != nil {
		return nil, err
	}
	model, err := service.user.Model(ctx)
	if err != nil {
		return nil, err
	}
	if service.runtime.Dialect().Kind() == driver.SQLite {
		if _, err = model.Data(loginUserWriteLock{ID: userID}).Where("id", userID).Update(); err != nil {
			return nil, exception.WrapCore(err, "锁定登录用户失败")
		}
		model, err = service.user.Model(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		model = model.LockUpdate()
	}
	var user *loginUserRow
	err = model.
		Fields("id", "username", "password", "passwordV", "status").
		Where("id", userID).
		Scan(&user)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, exception.WrapCore(err, "读取登录用户失败")
	}

	return user, nil
}

func (service *LoginService) adminPrincipal(ctx context.Context, user *loginUserRow) (auth.Principal, error) {
	roleIDs, err := service.permission.RoleIDs(ctx, user.ID)
	if err != nil {
		return auth.Principal{}, err
	}
	if len(roleIDs) == 0 {
		return auth.Principal{}, errRolesMissing
	}

	return auth.Principal{
		Subject: auth.AdminKind, UserID: user.ID, Username: user.Username,
		RoleIDs: roleIDs, PasswordV: int(user.PasswordV),
	}, nil
}

func (service *LoginService) updatePasswordHash(ctx context.Context, userID uint64, encoded string) error {
	model, err := service.user.Model(ctx)
	if err != nil {
		return err
	}
	if _, err = model.Data(loginPasswordUpdate{Password: encoded}).Where("id", userID).Update(); err != nil {
		return exception.WrapCore(err, "更新用户密码摘要失败")
	}

	return nil
}

func (service *LoginService) tokenResult(pair auth.Pair) dto.TokenResult {
	return dto.TokenResult{
		Token: pair.AccessToken, Expire: tokenTTL(pair.AccessExpiresAt),
		RefreshToken: pair.RefreshToken, RefreshExpire: tokenTTL(pair.ExpiresAt),
	}
}

func tokenTTL(expiresAt time.Time) int64 {
	remaining := time.Until(expiresAt)
	if remaining <= 0 {
		return 0
	}

	return int64((remaining + time.Second - 1) / time.Second)
}

func credentialErr() error {
	return exception.Comm("账户或密码不正确~")
}

func loginExpiredError() error {
	return exception.Comm("登录失效~", http.StatusUnauthorized)
}
