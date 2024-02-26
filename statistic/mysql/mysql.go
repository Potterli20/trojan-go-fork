package mysql

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	// MySQL Driver
	"github.com/go-sql-driver/mysql"
	"github.com/go-sql-driver/mysql"

	"gitlab.atcatw.org/atca/community-edition/trojan-go/common"
	"gitlab.atcatw.org/atca/community-edition/trojan-go/config"
	"gitlab.atcatw.org/atca/community-edition/trojan-go/log"
	"gitlab.atcatw.org/atca/community-edition/trojan-go/statistic"
	"gitlab.atcatw.org/atca/community-edition/trojan-go/statistic/memory"
)

const Name = "MYSQL"

type Authenticator struct {
	*memory.Authenticator
	db             *sql.DB
	updateDuration time.Duration
	ctx            context.Context
	wg             sync.WaitGroup
}

func (a *Authenticator) updater() {
	for {
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
		if err != nil || rows.Err() != nil {
			log.Error(common.NewError("failed to pull data from the database").Base(err))
			time.Sleep(a.updateDuration)
			continue
		}
		userMap := make(map[string]bool)
		for rows.Next() {
			var username, hash string
			var maxip int
			var quota, download, upload int64
			err := rows.Scan(&username, &hash, &quota, &download, &upload, &maxip)
			if err != nil {
				log.Error(common.NewError("failed to obtain data from the query result").Base(err))
				break
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
		for _, user := range a.ListUsers() {
			if _, ok := userMap[user.GetHash()]; !ok {
				a.DelUser(user.GetHash())
			}
		}

		select {
		case <-time.After(a.updateDuration):
		case <-a.ctx.Done():
			log.Debug("MySQL daemon exiting...")
			return
		}
	}
}

func connectDatabase(driverName, username, password, ip string, port int, dbName, keyPath, certPath, caPath string) (*sql.DB, error) {
	path := strings.Join([]string{username, ":", password, "@tcp(", ip, ":", fmt.Sprintf("%d", port), ")/", dbName, "?charset=utf8"}, "")

	// Adding support for TLS certs
	if caPath != "" {
		path += "&tls=custom"
		rootCertPool := x509.NewCertPool()
		pem, err := ioutil.ReadFile(caPath)
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
			return nil, errors.New("Set both key and cert, or set neither.")
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
		return nil, err
	}
	a := &Authenticator{
		db:             db,
		ctx:            ctx,
		updateDuration: time.Duration(cfg.MySQL.CheckRate) * time.Second,
		Authenticator:  memoryAuth.(*memory.Authenticator),
	}
	go a.updater()
	log.Debug("mysql authenticator created")
	return a, nil
}

func init() {
	statistic.RegisterAuthenticatorCreator(Name, NewAuthenticator)
}
