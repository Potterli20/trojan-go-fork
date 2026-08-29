package common

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
)

// SHA224String 计算 trojan 协议标准的密码哈希：hex(SHA224(password))。
// 客户端与服务端必须使用同一算法，不能换成 bcrypt 等带随机 salt 的算法，
// 否则双端计算结果不一致，配置文件密码认证将永远失败。
func SHA224String(password string) string {
	sum := sha256.Sum224([]byte(password))
	return hex.EncodeToString(sum[:])
}

type Runnable interface {
	Run() error
	Close() error
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func GetProgramDir() (string, error) {
	return filepath.Abs(filepath.Dir(os.Args[0]))
}

func GetAssetLocation(file string) (string, error) {
	if filepath.IsAbs(file) {
		return file, nil
	}
	if loc := os.Getenv("TROJAN_GO_LOCATION_ASSET"); loc != "" {
		absPath, err := filepath.Abs(loc)
		if err != nil {
			return "", err
		}
		return filepath.Join(absPath, file), nil
	}
	dir, err := GetProgramDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, file), nil
}
