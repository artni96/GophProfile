package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Config struct {
	ServerAddr string `env:"SERVER_ADDR"`
	DBDsn      string `env:"DB_DSN"`
	DBName     string `env:"DB_NAME"`
	S3         *s3Config
}

type dbConfig struct {
	DBName     string `env:"DB_NAME"`
	DBHost     string `env:"DB_HOST"`
	DBPort     uint64 `env:"DB_PORT"`
	DBUser     string `env:"DB_USER"`
	DBPassword string `env:"DB_PASSWORD"`
	SSLMode    string `env:"SSL_MODE"`
}

type s3Config struct {
	Region            string `env:"AWS_REGION"`
	Endpoint          string `env:"AWS_ENDPOINT"`
	S3ForcePathStyle  *bool
	Credentials       *credentials.Credentials
	AccessKey         string `env:"AWS_ACCESS_KEY"`
	SecretKey         string `env:"AWS_SECRET_KEY"`
	DisableSSL        bool   `env:"DISABLE_SSL"`
	MinioRootUser     string `env:"MINIO_ROOT_USER"`
	MinioRootPassword string `env:"MINIO_ROOT_PASSWORD"`
	BucketName        string `env:"BUCKET_NAME"`
}

func newS3Config() (*s3Config, error) {
	cfg := &s3Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	cfg.S3ForcePathStyle = aws.Bool(true)
	cfg.Credentials = credentials.NewStaticCredentials(cfg.AccessKey, cfg.SecretKey, "")
	return cfg, nil
}

func NewConfig() (*Config, error) {
	fs := flag.NewFlagSet("fs", flag.ExitOnError)

	var cfg Config
	var dbCfg dbConfig
	declaredFlags := make(map[string]bool)
	var errs []string

	fs.StringVar(&cfg.ServerAddr, "a", ":8080", "server address")
	fs.StringVar(&dbCfg.DBName, "dn", "", "database name")
	fs.StringVar(&dbCfg.DBHost, "dh", "", "database host")
	fs.Uint64Var(&dbCfg.DBPort, "dp", 0, "database port")
	fs.StringVar(&dbCfg.DBUser, "du", "", "database user")
	fs.StringVar(&dbCfg.DBPassword, "dpw", "", "database password")
	fs.StringVar(&dbCfg.SSLMode, "ds", "", "database ssl mode")

	err := fs.Parse(os.Args[1:])
	if err != nil {
		return nil, err
	}

	err = godotenv.Load(".env")
	if err != nil {
		return nil, fmt.Errorf("failed to load .env files")
	}

	if !declaredFlags["a"] {
		envSerAddr, ok := os.LookupEnv("SERVER_ADDR")
		if ok {
			cfg.ServerAddr = envSerAddr
		} else {
			errs = append(errs, "SERVER_ADDR is required")
		}
	}

	if !declaredFlags["dn"] {
		envDBName, ok := os.LookupEnv("DB_NAME")
		if ok {
			dbCfg.DBName = envDBName
			cfg.DBName = envDBName
		} else {
			errs = append(errs, "DB_NAME is required")
		}
	}

	if !declaredFlags["dh"] {
		envDBHost, ok := os.LookupEnv("DB_HOST")
		if ok {
			dbCfg.DBHost = envDBHost
		} else {
			errs = append(errs, "DB_HOST is required")
		}
	}

	if !declaredFlags["dp"] {
		envDBPort, ok := os.LookupEnv("DB_PORT")
		if ok {
			convertDBPort, err := strconv.ParseUint(envDBPort, 10, 64)
			if err != nil {
				errs = append(errs, "failed to parse DB_PORT")
			} else {
				dbCfg.DBPort = convertDBPort
			}
		} else {
			errs = append(errs, "DB_PORT is required")
		}
	}

	if !declaredFlags["du"] {
		envDBUser, ok := os.LookupEnv("DB_USER")
		if ok {
			dbCfg.DBUser = envDBUser
		} else {
			errs = append(errs, "DB_USER is required")
		}
	}

	if !declaredFlags["dpw"] {
		envDBPassword, ok := os.LookupEnv("DB_PASSWORD")
		if ok {
			dbCfg.DBPassword = envDBPassword
		} else {
			errs = append(errs, "DB_PASSWORD is required")
		}
	}

	if !declaredFlags["ds"] {
		envSSLMode, ok := os.LookupEnv("SSL_MODE")
		if ok {
			dbCfg.SSLMode = envSSLMode
		} else {
			errs = append(errs, "SSL_MODE is required")
		}
	}

	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "\n"))
	}
	cfg.setDBDsn(&dbCfg)
	s3cfg, err := newS3Config()
	if err != nil {
		return nil, fmt.Errorf("failed to init s3 config")
	}
	cfg.S3 = s3cfg
	return &cfg, nil
}

func (c *Config) setDBDsn(dbCfg *dbConfig) {
	c.DBDsn = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		dbCfg.DBHost, dbCfg.DBPort, dbCfg.DBUser, dbCfg.DBPassword, dbCfg.DBName, dbCfg.SSLMode)

}
