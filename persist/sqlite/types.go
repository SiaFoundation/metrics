package sqlite

import (
	"bytes"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"time"

	"go.sia.tech/core/types"
)

type (
	sqlTime time.Time
)

func (st sqlTime) Value() (driver.Value, error) {
	return time.Time(st).UnixMilli(), nil
}

func (st *sqlTime) Scan(src any) error {
	if t, ok := src.(int64); ok {
		*st = sqlTime(time.UnixMilli(t))
		return nil
	}
	return errors.New("invalid type")
}

type sqlHash256 types.Hash256

func (h sqlHash256) Value() (driver.Value, error) {
	return h[:], nil
}

func (h *sqlHash256) Scan(src any) error {
	if b, ok := src.([]byte); ok {
		copy((*h)[:], b)
		return nil
	}
	return errors.New("invalid type")
}

type sqlCurrency types.Currency

func (c sqlCurrency) Value() (driver.Value, error) {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b, c.Hi)
	binary.BigEndian.PutUint64(b[8:], c.Lo)
	return b, nil
}

func (c *sqlCurrency) Scan(src any) error {
	if b, ok := src.([]byte); ok && len(b) == 16 {
		c.Hi = binary.BigEndian.Uint64(b)
		c.Lo = binary.BigEndian.Uint64(b[8:])
		return nil
	}
	return errors.New("invalid type")
}

type sqlEncodable[T types.EncoderTo] struct {
	value T
}

type sqlDecodable[T types.DecoderFrom] struct {
	value T
}

func (e sqlEncodable[T]) Value() (driver.Value, error) {
	buf := bytes.NewBuffer(nil)
	enc := types.NewEncoder(buf)
	e.value.EncodeTo(enc)
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (e *sqlDecodable[T]) Scan(src any) error {
	if b, ok := src.([]byte); ok {
		dec := types.NewBufDecoder(b)
		e.value.DecodeFrom(dec)
		return dec.Err()
	}
	return errors.New("invalid type")
}

func encodable[T types.EncoderTo](v T) sqlEncodable[T] {
	return sqlEncodable[T]{value: v}
}

func decodable[T types.DecoderFrom](v T) *sqlDecodable[T] {
	return &sqlDecodable[T]{value: v}
}
