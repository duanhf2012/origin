package aesencrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

func NewAesEncrypt(key string) (aes *AesEncrypt, err error) {
	keyLen := len(key)
	if keyLen < 16 {
		err = fmt.Errorf("the length of res key shall not be less than 16")
		return
	}
	aes = &AesEncrypt{
		StrKey: key,
	}
	return aes, nil
}

type AesEncrypt struct {
	StrKey string
}

func (ae *AesEncrypt) getKey() []byte {
	keyLen := len(ae.StrKey)
	if keyLen < 16 {
		panic("The length of res key shall not be less than 16")
	}
	arrKey := []byte(ae.StrKey)
	if keyLen >= 32 {
		//取前32个字节
		return arrKey[:32]
	}
	if keyLen >= 24 {
		//取前24个字节
		return arrKey[:24]
	}
	//取前16个字节
	return arrKey[:16]
}

// Encrypt 加密字符串
func (ae *AesEncrypt) Encrypt(str string) ([]byte, error) {
	key := ae.getKey()
	var iv = key[:aes.BlockSize]
	encrypted := make([]byte, len(str))
	aesBlockEncrypter, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aesEncrypter := cipher.NewCFBEncrypter(aesBlockEncrypter, iv)
	aesEncrypter.XORKeyStream(encrypted, []byte(str))
	return encrypted, nil
}

// Decrypt 解密字符串
func (ae *AesEncrypt) Decrypt(src []byte) (strDesc string, err error) {
	defer func() {
		//错误处理
		if e := recover(); e != nil {
			err = e.(error)
		}
	}()
	key := ae.getKey()
	var iv = key[:aes.BlockSize]
	decrypted := make([]byte, len(src))
	var aesBlockDecrypter cipher.Block
	aesBlockDecrypter, err = aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aesDecrypter := cipher.NewCFBDecrypter(aesBlockDecrypter, iv)
	aesDecrypter.XORKeyStream(decrypted, src)
	return string(decrypted), nil
}
