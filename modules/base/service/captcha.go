package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"strings"

	svg "github.com/reu98/go-svg-captcha"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/risk"
	"github.com/toothdy/cool-admin-go-next/modules/base/dto"
)

const (
	captchaCodeLength   = 4                                                                // 验证码长度
	captchaIDBytes      = 16                                                               // 验证码标识字节数
	captchaDefaultWidth = 150                                                              // 默认宽度
	captchaHeight       = 50                                                               // 默认高度
	captchaDefaultColor = "#fff"                                                           // 默认文字颜色
	captchaMinWidth     = 30                                                               // 最小宽度
	captchaMaxWidth     = 1000                                                             // 最大宽度
	captchaMinHeight    = 20                                                               // 最小高度
	captchaMaxHeight    = 500                                                              // 最大高度
	captchaCharacters   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789" // 验证码字符集
	captchaCurveOpacity = "0.35"                                                           // 干扰线透明度
	captchaCurveWidth   = "1"                                                              // 干扰线宽度
)

type captchaSVG struct {
	XMLName xml.Name         `xml:"svg"`
	XMLNS   string           `xml:"xmlns,attr"`
	Width   int              `xml:"width,attr"`
	Height  int              `xml:"height,attr"`
	ViewBox string           `xml:"viewBox,attr"`
	Style   string           `xml:"style,attr,omitempty"`
	Paths   []captchaSVGPath `xml:"path"`
}

type captchaSVGPath struct {
	Fill          string `xml:"fill,attr,omitempty"`
	Data          string `xml:"d,attr"`
	Stroke        string `xml:"stroke,attr,omitempty"`
	StrokeWidth   string `xml:"stroke-width,attr,omitempty"`
	StrokeOpacity string `xml:"stroke-opacity,attr,omitempty"`
	StrokeLinecap string `xml:"stroke-linecap,attr,omitempty"`
}

// 模块图形验证码服务
type CaptchaService struct {
	color  string
	height int
	random io.Reader
	risk   *risk.Service
	width  int
}

// 使用固定显示参数创建验证码服务
func NewCaptcha(riskService *risk.Service) *CaptchaService {
	return &CaptchaService{
		color:  captchaDefaultColor,
		height: captchaHeight,
		random: rand.Reader,
		risk:   riskService,
		width:  captchaDefaultWidth,
	}
}

// 生成 SVG Path 验证码并保存一次性答案
func (service *CaptchaService) Generate(ctx context.Context, clientIP string, query dto.CaptchaQuery) (dto.CaptchaResult, error) {
	if err := service.risk.AllowCaptcha(ctx, clientIP); err != nil {
		return dto.CaptchaResult{}, err
	}
	width, height, color := service.normalizeOptions(query)
	code, err := service.randomString(captchaCodeLength, captchaCharacters)
	if err != nil {
		return dto.CaptchaResult{}, exception.WrapCore(err, "生成验证码失败")
	}
	captchaID, err := service.randomHex(captchaIDBytes)
	if err != nil {
		return dto.CaptchaResult{}, exception.WrapCore(err, "生成验证码标识失败")
	}
	image, err := buildCaptchaSVG(code, width, height, color)
	if err != nil {
		return dto.CaptchaResult{}, exception.WrapCore(err, "生成验证码 SVG 失败")
	}
	if err = service.risk.SaveCaptcha(ctx, clientIP, captchaID, code); err != nil {
		return dto.CaptchaResult{}, err
	}
	return dto.CaptchaResult{
		CaptchaID: captchaID,
		Data:      "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(image),
	}, nil
}

// 校验验证码
func (service *CaptchaService) Verify(ctx context.Context, captchaID, verifyCode string) (bool, error) {
	if captchaID == "" || verifyCode == "" {
		return false, nil
	}
	return service.risk.ConsumeCaptcha(ctx, captchaID, verifyCode)
}

func (service *CaptchaService) randomString(length int, characters string) (string, error) {
	var builder strings.Builder
	builder.Grow(length)
	for index := 0; index < length; index++ {
		randomIndex, err := service.randomInt(len(characters))
		if err != nil {
			return "", err
		}
		builder.WriteByte(characters[randomIndex])
	}

	return builder.String(), nil
}

func (service *CaptchaService) randomInt(max int) (int, error) {
	if max <= 0 {
		return 0, fmt.Errorf("随机上限必须大于零")
	}
	value, err := rand.Int(service.random, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}

	return int(value.Int64()), nil
}

func (service *CaptchaService) randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(service.random, value); err != nil {
		return "", err
	}

	return hex.EncodeToString(value), nil
}

func (service *CaptchaService) normalizeOptions(query dto.CaptchaQuery) (int, int, string) {
	width := query.Width
	if width < captchaMinWidth || width > captchaMaxWidth {
		width = service.width
	}
	height := query.Height
	if height < captchaMinHeight || height > captchaMaxHeight {
		height = service.height
	}
	color := query.Color
	if !isCaptchaColor(color) {
		color = service.color
	}

	return width, height, color
}

func isCaptchaColor(color string) bool {
	if (len(color) != 4 && len(color) != 7) || color[0] != '#' {
		return false
	}
	for _, character := range color[1:] {
		isNumber := character >= '0' && character <= '9'
		isLower := character >= 'a' && character <= 'f'
		isUpper := character >= 'A' && character <= 'F'
		if !isNumber && !isLower && !isUpper {
			return false
		}
	}

	return true
}

func buildCaptchaSVG(code string, width, height int, value string) ([]byte, error) {
	result, err := svg.CreateByText(svg.OptionText{
		Text:   code,
		Width:  uint16(width),
		Height: uint16(height),
		Curve:  1,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("SVG 验证码结果为空")
	}

	var source captchaSVG
	if err = xml.Unmarshal([]byte(result.Data), &source); err != nil {
		return nil, err
	}

	paths := make([]captchaSVGPath, 0, len(code)+1)
	for _, path := range source.Paths {
		if path.Data == "" {
			continue
		}
		if path.Stroke != "" {
			if len(paths) == 0 {
				paths = append(paths, captchaSVGPath{
					Fill:          "none",
					Data:          path.Data,
					Stroke:        value,
					StrokeWidth:   captchaCurveWidth,
					StrokeOpacity: captchaCurveOpacity,
					StrokeLinecap: "round",
				})
			}
			continue
		}
		paths = append(paths, captchaSVGPath{Fill: value, Data: path.Data})
	}
	if len(paths) != len(code)+1 || paths[0].Stroke == "" {
		return nil, fmt.Errorf("SVG 验证码 Path 结构无效")
	}

	return xml.Marshal(captchaSVG{
		XMLNS:   "http://www.w3.org/2000/svg",
		Width:   width,
		Height:  height,
		ViewBox: fmt.Sprintf("0 0 %d %d", width, height),
		Style:   "transform: rotateX(180deg)",
		Paths:   paths,
	})
}
