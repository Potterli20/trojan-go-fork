package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"strings"
	"sync"
	"time"

	// MySQL Driver
	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/statistic"
	"github.com/Potterli20/trojan-go-fork/statistic/memory"
)

const Name = "MYSQL"

type Authenticator struct {
	*memory.Authenticator
	db             *sql.DB
	updateDuration time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func (a *Authenticator) updater() {
	ticker := time.NewTicker(a.updateDuration)
	defer ticker.Stop()

	for {
		a.syncUsers()

		select {
		case <-ticker.C:
		case <-a.ctx.Done():
			log.Debug("MySQL daemon exiting...")
			return
		}
	}
}

// syncUsers 将内存中的流量增量写入数据库，并从数据库同步用户列表到内存。
// 查询中途出错时直接放弃本轮同步：userMap 不完整，据此清理会误删合法用户。
func (a *Authenticator) syncUsers() {
	for _, user := range a.ListUsers() {
		// swap upload and download for users
		hash := user.GetHash()
		sent, recv := user.ResetTraffic()

		s, err := a.db.Exec("UPDATE `users` SET `upload`=`upload`+?, `download`=`download`+? WHERE `password`=?;", recv, sent, hash)
		if err != nil {
			log.Error(common.NewError("failed to update data to user table").Base(err))
			continue
		}
		if r, err := s.RowsAffected(); err == nil && r != 1 {
			a.DelUser(hash)
		}
	}
	log.Info("buffered data has been written into the database")

	// update memory
	rows, err := a.db.Query("SELECT username,password,quota,download,upload,maxip FROM users")
	if err != nil {
		log.Error(common.NewError("failed to pull data from the database").Base(err))
		return
	}
	defer rows.Close()

	userMap := make(map[string]bool)
	for rows.Next() {
		var username, hash string
		var maxip int
		var quota, download, upload int64
		err := rows.Scan(&username, &hash, &quota, &download, &upload, &maxip)
		if err != nil {
			log.Error(common.NewError("failed to obtain data from the query result").Base(err))
			return
		}
		userMap[hash] = true
		if download+upload < quota || quota < 0 {
			a.AddUser(hash)
			a.SetKeyShare(hash, username)
			a.SetUserIPLimit(hash, maxip)
		} else {
			a.DelUser(hash)
		}
	}
	if err := rows.Err(); err != nil {
		log.Error(common.NewError("failed to iterate users from the database").Base(err))
		return
	}
	for _, user := range a.ListUsers() {
		if _, ok := userMap[user.GetHash()]; !ok {
			a.DelUser(user.GetHash())
		}
	}
}

func (a *Authenticator) Close() error {
	// 顺序至关重要：必须先停本层 updater 再关内层 memory 认证器。
	// 若先关用户，ResetTraffic 清零后仍在运行的 updater 会把下溢值写进数据库；
	// 且 updater 每轮都会撞上这种脏数据。
	a.cancel()
	a.wg.Wait()
	if err := a.Authenticator.Close(); err != nil {
		log.Error(common.NewError("mysql failed to close memory authenticator").Base(err))
	}
	return a.db.Close()
}

func connectDatabase(driverName, username, password, ip string, port int, dbName, keyPath, certPath, caPath string) (*sql.DB, error) {
	path := strings.Join([]string{username, ":", password, "@tcp(", ip, ":", fmt.Sprintf("%d", port), ")/", dbName, "?charset=utf8"}, "")

	// Adding support for TLS certs
	if caPath != "" {
		path += "&tls=custom"
		rootCertPool := x509.NewCertPool()
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, err
		}
		if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
			return nil, errors.New("AppendCertsFromPEM() failed")
		}
		if keyPath != "" && certPath != "" {
			// Both Key and Cert are set. Go with customer cert.
			clientCert := make([]tls.Certificate, 0, 1)
			certs, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err != nil {
				return nil, err
			}
			clientCert = append(clientCert, certs)
			mysql.RegisterTLSConfig("custom", &tls.Config{
				// ServerName: "example.com",
				RootCAs:      rootCertPool,
				Certificates: clientCert,
				MinVersion:   tls.VersionTLS12,
				MaxVersion:   0,
			})
		} else if keyPath == "" && certPath == "" {
			// Neither Key or Cert is set. Proceed without customer cert.
			mysql.RegisterTLSConfig("custom", &tls.Config{
				// ServerName: "example.com",
				RootCAs:    rootCertPool,
				MinVersion: tls.VersionTLS12,
				MaxVersion: 0,
			})
		} else {
			// one of Key or Cert is set but not both, which is ILLEGAL.
			return nil, errors.New("set both key and cert, or set neither")
		}
	}

	return sql.Open(driverName, path)
}

func NewAuthenticator(ctx context.Context) (statistic.Authenticator, error) {
	cfg := config.FromContext(ctx, Name).(*Config)
	db, err := connectDatabase(
		"mysql",
		cfg.MySQL.Username,
		cfg.MySQL.Password,
		cfg.MySQL.ServerHost,
		cfg.MySQL.ServerPort,
		cfg.MySQL.Database,
		cfg.MySQL.Key,
		cfg.MySQL.Cert,
		cfg.MySQL.CA,
	)
	if err != nil {
		return nil, common.NewError("Failed to connect to database server").Base(err)
	}
	memoryAuth, err := memory.NewAuthenticator(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	// time.NewTicker 对非正周期会 panic，给 check_rate 一个下限
	updateDuration := time.Duration(cfg.MySQL.CheckRate) * time.Second
	if updateDuration <= 0 {
		updateDuration = 30 * time.Second
	}
	// 派生自有 ctx：updater 的生命周期由自身 Close 控制，不依赖调用方取消父 ctx
	ctx, cancel := context.WithCancel(ctx)
	a := &Authenticator{
		db:             db,
		ctx:            ctx,
		cancel:         cancel,
		updateDuration: updateDuration,
		Authenticator:  memoryAuth.(*memory.Authenticator),
	}
	// 用 wg.Go 而非裸 go：Close() 的 wg.Wait() 必须能等到 updater 退出
	a.wg.Go(a.updater)
	log.Debug("mysql authenticator created")
	return a, nil
}

func init() {
	statistic.RegisterAuthenticatorCreator(Name, NewAuthenticator)
}
