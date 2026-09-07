package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func InitLogger(level string) (*zap.Logger, error) {
	levelMap := map[string]zapcore.Level{
		"debug": zapcore.DebugLevel,
		"info":  zapcore.InfoLevel,
		"warn":  zapcore.WarnLevel,
		"error": zapcore.ErrorLevel,
		"fatal": zapcore.FatalLevel,
	}

	consoleEncoder := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	logFileConfig := zap.NewDevelopmentEncoderConfig()
	logFileConfig.EncodeCaller = zapcore.FullCallerEncoder
	fileEncoder := zapcore.NewJSONEncoder(logFileConfig)

	logFile, err := os.OpenFile("./internal/logger/shortener.log", os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}

	fileOut := zapcore.NewCore(fileEncoder, zapcore.AddSync(logFile), levelMap[level])
	stdOut := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), levelMap[level])

	loggerCore := zapcore.NewTee(fileOut, stdOut)
	logger := zap.New(loggerCore, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	defer logger.Sync()

	return logger, nil
}
