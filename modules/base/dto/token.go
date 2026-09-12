package dto

// 令牌请求
type RefreshReq struct {
	RefreshToken string `json:"refreshToken" v:"required"`
}

// 登录和刷新共用的令牌响应
type TokenResult struct {
	Token         string `json:"token"`
	Expire        int64  `json:"expire"`
	RefreshToken  string `json:"refreshToken"`
	RefreshExpire int64  `json:"refreshExpire"`
}
