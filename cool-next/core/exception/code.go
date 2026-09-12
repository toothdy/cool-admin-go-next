package exception

const (
	Success      = 1000 // 操作成功
	CommFail     = 1001 // 通用失败
	ValidateFail = 1002 // 参数校验失败
	CoreFail     = 1003 // 核心服务失败
)

const (
	MsgSuccess      = "success"       // 操作成功消息
	MsgCommFail     = "comm fail"     // 通用失败消息
	MsgValidateFail = "validate fail" // 参数校验失败消息
	MsgCoreFail     = "core fail"     // 核心服务失败消息
)

const (
	CommException     = "CoolCommException"     // 通用业务异常
	ValidateException = "CoolValidateException" // 参数校验异常
	CoreException     = "CoolCoreException"     // 核心服务异常
)
