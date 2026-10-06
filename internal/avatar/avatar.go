// Package avatar validates small, self-contained profile photos.
package avatar

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/jpeg"
	"strings"
)

const MaxBytes = 32 << 10
const MaxSize = 128
const Prefix = "data:image/jpeg;base64,"

func Validate(value string) error {
	if value == "" {
		return nil
	}
	invalid := errors.New("写真は128×128px以下の小さなJPEG画像を選んでください")
	if !strings.HasPrefix(value, Prefix) || len(value) > len(Prefix)+base64.StdEncoding.EncodedLen(MaxBytes) {
		return invalid
	}
	data, err := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(value, Prefix))
	if err != nil || len(data) > MaxBytes {
		return invalid
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > MaxSize || config.Height > MaxSize {
		return invalid
	}
	if _, err = jpeg.Decode(bytes.NewReader(data)); err != nil {
		return invalid
	}
	return nil
}
