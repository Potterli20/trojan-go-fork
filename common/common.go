package common

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"

	"github.com/Potterli20/trojan-go-fork/log"
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

// SHA224String is kept for backward compatibility
func SHA224String(password string) string {
	hash := sha256.New224()
	hash.Write([]byte(password))
	val := hash.Sum(nil)
	str := ""
	for _, v := range val {
		str += fmt.Sprintf("%02x", v)
	}
	return str
}

func GetProgramDir() string {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatal(err)
	}
	return dir
}

func GetAssetLocation(file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	if loc := os.Getenv("TROJAN_GO_LOCATION_ASSET"); loc != "" {
		absPath, err := filepath.Abs(loc)
		if err != nil {
			log.Fatal(err)
		}
		log.Debugf("env set: TROJAN_GO_LOCATION_ASSET=%s", absPath)
		return filepath.Join(absPath, file)
	}
	return filepath.Join(GetProgramDir(), file)
}
