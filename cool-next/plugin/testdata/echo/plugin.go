package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/toothdy/cool-admin-go-next/cool-next/plugin/sdk"
)

type echoConfig struct {
	Prefix string `json:"prefix"`
}

type echoRequest struct {
	Value string `json:"value"`
}

type echoResponse struct {
	Value string `json:"value"`
}

type uploadRequest struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type uploadResponse struct {
	URL  string `json:"url,omitempty"`
	Mode string `json:"mode"`
	Type string `json:"type"`
}

func init() {
	sdk.Register(sdk.Define(
		sdk.Method("echo", echo),
		sdk.Method("readData", readData),
		sdk.Method("upload", upload),
		sdk.Method("mode", uploadMode),
		sdk.RawMethod("host", callHost),
		sdk.RawMethod("panic", panicMethod),
		sdk.RawMethod("loop", loop),
	))
}

func upload(ctx context.Context, request uploadRequest) (uploadResponse, error) {
	data, err := os.ReadFile(request.Path)
	if err != nil {
		return uploadResponse{}, err
	}
	if int64(len(data)) != request.Size {
		return uploadResponse{}, errors.New("upload size mismatch")
	}
	if string(data) == "fail" {
		return uploadResponse{}, errors.New("upload failed")
	}
	config, err := sdk.Config[echoConfig](ctx)
	if err != nil {
		return uploadResponse{}, err
	}
	digest := sha256.Sum256(data)

	return uploadResponse{
		URL:  "https://upload.example/" + config.Prefix + hex.EncodeToString(digest[:]),
		Mode: "cloud",
		Type: "wasm",
	}, nil
}

func uploadMode(context.Context, struct{}) (uploadResponse, error) {
	return uploadResponse{Mode: "cloud", Type: "wasm"}, nil
}

func readData(_ context.Context, request struct {
	Path string `json:"path"`
}) (echoResponse, error) {
	data, err := os.ReadFile(request.Path)
	if err != nil {
		return echoResponse{}, err
	}

	return echoResponse{Value: string(data)}, nil
}

func echo(ctx context.Context, request echoRequest) (echoResponse, error) {
	config, err := sdk.Config[echoConfig](ctx)
	if err != nil {
		return echoResponse{}, err
	}

	return echoResponse{Value: config.Prefix + request.Value}, nil
}

func callHost(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return sdk.HostCall(ctx, "echo.prefix", input)
}

func panicMethod(context.Context, json.RawMessage) (json.RawMessage, error) {
	panic("guest panic")
}

func loop(context.Context, json.RawMessage) (json.RawMessage, error) {
	for {
	}
}
