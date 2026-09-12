// Package bcrypt 提供框架密码摘要边界
package bcrypt

import (
	"errors"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	cryptobcrypt "golang.org/x/crypto/bcrypt"
)

// 默认 bcrypt 摘要计算成本
const DefaultCost = 12 // 默认摘要计算成本

// bcrypt 配置
type Config struct {
	Cost int `json:"cost"` // 摘要计算成本
}

// bcrypt 密码适配器
type Verifier struct {
	cost int
}

// 密码校验结果
type VerifyResult struct {
	Valid       bool // 密码是否匹配
	NeedsRehash bool // 是否需要按目标成本重新生成摘要
}

// 创建密码适配器
func New(config Config) (*Verifier, error) {
	cost := config.Cost
	if cost == 0 {
		cost = DefaultCost
	}
	if cost < cryptobcrypt.MinCost || cost > cryptobcrypt.MaxCost {
		return nil, exception.Core("bcrypt Cost 必须在 4 到 31 之间")
	}

	return &Verifier{cost: cost}, nil
}

// 生成密码摘要
func (verifier *Verifier) Hash(password string) (string, error) {
	if verifier == nil {
		return "", exception.Core("bcrypt 密码适配器未初始化")
	}

	encoded, err := cryptobcrypt.GenerateFromPassword([]byte(password), verifier.cost)
	if err != nil {
		return "", exception.WrapCore(err, "生成密码摘要失败")
	}

	return string(encoded), nil
}

// 校验密码摘要
func (verifier *Verifier) Verify(password, encoded string) (VerifyResult, error) {
	if verifier == nil {
		return VerifyResult{}, exception.Core("bcrypt 密码适配器未初始化")
	}

	err := cryptobcrypt.CompareHashAndPassword([]byte(encoded), []byte(password))
	if errors.Is(err, cryptobcrypt.ErrMismatchedHashAndPassword) {
		return VerifyResult{}, nil
	}
	if err != nil {
		return VerifyResult{}, exception.WrapCore(err, "校验密码摘要失败")
	}

	cost, err := cryptobcrypt.Cost([]byte(encoded))
	if err != nil {
		return VerifyResult{}, exception.WrapCore(err, "读取密码摘要成本失败")
	}

	return VerifyResult{Valid: true, NeedsRehash: cost != verifier.cost}, nil
}
