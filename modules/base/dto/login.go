package dto

// 后台登录请求
type LoginReq struct {
	Username   string `json:"username" v:"required"`
	Password   string `json:"password" v:"required"`
	CaptchaID  string `json:"captchaId" v:"required"`
	VerifyCode string `json:"verifyCode" v:"required"`
}

// 验证码显示参数
type CaptchaQuery struct {
	Width  int    `json:"width" in:"query"`
	Height int    `json:"height" in:"query"`
	Color  string `json:"color" in:"query"`
}

// 验证码响应
type CaptchaResult struct {
	CaptchaID string `json:"captchaId"`
	Data      string `json:"data"`
}
