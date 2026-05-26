package common

import (
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
)

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
