// Package promptpay generates PromptPay QR payload (EMVCo format) and PNG image
package promptpay

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/skip2/go-qrcode"
)

// QRBase64 returns a base64-encoded PNG of the PromptPay QR for the given phone number and amount.
// Pass amount <= 0 to generate a QR without a fixed amount.
func QRBase64(phone string, amount float64) (string, error) {
	payload := buildPayload(phone, amount)
	png, err := qrcode.Encode(payload, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(png), nil
}

// QRPng returns raw PNG bytes.
func QRPng(phone string, amount float64) ([]byte, error) {
	payload := buildPayload(phone, amount)
	q, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	return q.PNG(256)
}

func buildPayload(phone string, amount float64) string {
	target := formatPhone(phone)

	// merchant account info: AID + phone
	merchantInfo := "0016A000000677010111" + "0113" + target
	merchantTag := tlv("29", merchantInfo)

	p := "000201" + // payload format indicator
		"010211" + // dynamic QR
		merchantTag +
		"5303764" + // THB
		amountField(amount) +
		"5802TH" +
		"6304"
	p += crc16(p)
	return p
}

func formatPhone(s string) string {
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	if len(s) == 10 && s[0] == '0' {
		return "0066" + s[1:]
	}
	return s
}

func amountField(amount float64) string {
	if amount <= 0 {
		return ""
	}
	v := fmt.Sprintf("%.2f", amount)
	return tlv("54", v)
}

func tlv(tag, value string) string {
	return tag + fmt.Sprintf("%02d", len(value)) + value
}

func crc16(payload string) string {
	crc := uint16(0xFFFF)
	for _, b := range []byte(payload) {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return fmt.Sprintf("%04X", crc)
}
