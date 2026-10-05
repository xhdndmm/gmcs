package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gmcs/internal/protocol"
)

// errSessionRejected 表示会话服务器确认该玩家未通过正版认证。
var errSessionRejected = errors.New("session server rejected the account")

// playerProfile 是会话服务器返回的玩家档案。
type playerProfile struct {
	UUID       [16]byte
	Name       string
	Properties []protocol.GameProfileProperty
}

// verifySession 调用会话服务器（hasJoined）验证玩家已通过 Mojang 认证。
// serverHash 是由 protocol.ServerHash 计算的 serverId。
func (s *Server) verifySession(username, serverHash string) (*playerProfile, error) {
	endpoint := strings.TrimSuffix(s.config.SessionServerURL, "/") + "/session/minecraft/hasJoined"
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	query := request.URL.Query()
	query.Set("username", username)
	query.Set("serverId", serverHash)
	request.URL.RawQuery = query.Encode()

	response, err := s.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求会话服务器：%w", err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		// 继续解析。
	case http.StatusNoContent:
		return nil, errSessionRejected
	default:
		return nil, fmt.Errorf("会话服务器返回状态码 %d", response.StatusCode)
	}

	var payload struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Properties []struct {
			Name      string `json:"name"`
			Value     string `json:"value"`
			Signature string `json:"signature"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析会话服务器响应：%w", err)
	}
	uuid, err := parseUndashedUUID(payload.ID)
	if err != nil {
		return nil, err
	}
	profile := &playerProfile{UUID: uuid, Name: payload.Name}
	for _, property := range payload.Properties {
		profile.Properties = append(profile.Properties, protocol.GameProfileProperty{
			Name:      property.Name,
			Value:     property.Value,
			Signature: property.Signature,
			Signed:    property.Signature != "",
		})
	}
	return profile, nil
}

// parseUndashedUUID 解析十六进制 UUID 字符串（可带连字符）。
func parseUndashedUUID(value string) ([16]byte, error) {
	var uuid [16]byte
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return uuid, fmt.Errorf("无效的 UUID：%q", value)
	}
	if _, err := hex.Decode(uuid[:], []byte(compact)); err != nil {
		return uuid, fmt.Errorf("无效的 UUID：%q", value)
	}
	return uuid, nil
}
