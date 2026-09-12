package grpc

import (
	"errors"
	"net/http"
	"strconv"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

const errorDomain = "cool-admin"

// 将领域错误转换为安全的 gRPC 状态
func Error(err error) error {
	if err == nil {
		return nil
	}

	code, name, result := resolveError(err)
	value := status.New(code, result.Message)
	detail := &errdetails.ErrorInfo{
		Reason: name,
		Domain: errorDomain,
		Metadata: map[string]string{
			"code":    strconv.Itoa(result.Code),
			"message": result.Message,
		},
	}
	withDetails, detailErr := value.WithDetails(detail)
	if detailErr != nil {
		return value.Err()
	}

	return withDetails.Err()
}

func resolveError(err error) (codes.Code, string, exception.Result) {
	result := exception.Resolve(err)
	var coolError *exception.BaseException
	if !errors.As(err, &coolError) || coolError == nil {
		return codes.Internal, exception.CommException, result
	}

	switch {
	case coolError.Name == exception.ValidateException && result.Code == exception.ValidateFail:
		return codes.InvalidArgument, coolError.Name, result
	case coolError.Name == exception.CoreException && result.Code == exception.CoreFail:
		return codes.Internal, coolError.Name, result
	case coolError.Name == exception.CommException && result.Code == exception.CommFail:
		switch result.StatusCode {
		case http.StatusUnauthorized:
			return codes.Unauthenticated, coolError.Name, result
		case http.StatusForbidden:
			return codes.PermissionDenied, coolError.Name, result
		default:
			return codes.FailedPrecondition, coolError.Name, result
		}
	default:
		return codes.Internal, exception.CommException, exception.Resolve(errors.New("unknown Cool exception"))
	}
}
