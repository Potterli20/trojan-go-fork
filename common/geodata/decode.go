// Package geodata includes utilities to decode and parse the geoip & geosite dat files.
//
// It relies on the proto structure of GeoIP, GeoIPList, GeoSite and GeoSiteList in
// github.com/v2fly/v2ray-core/v4/app/router/config.proto to comply with following rules:
//
//  1. GeoIPList and GeoSiteList cannot be changed
//  2. The country_code in GeoIP and GeoSite must be
//     a length-delimited `string`(wired type) and has field_number set to 1
package geodata

import (
	"errors"
	"io"
	"os"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

var (
	ErrFailedToReadBytes            = errors.New("failed to read bytes")
	ErrFailedToReadExpectedLenBytes = errors.New("failed to read expected length of bytes")
	ErrInvalidGeodataFile           = errors.New("invalid geodata file")
	ErrInvalidGeodataVarintLength   = errors.New("invalid geodata varint length")
	// ErrInvalidGeodataLength 表示条目自称的长度超出了文件剩余字节数。必须有这一条：
	// 解析器要分配的字节数直接来自文件里的 varint，不做约束就等于让一个损坏（或恶意）
	// 的 .dat 决定本进程分配多大内存 —— make([]byte, 1<<60) 会直接把进程炸掉，
	// 而 router 是在启动时按配置加载 geoip.dat / geosite.dat 的。
	ErrInvalidGeodataLength = errors.New("invalid geodata entry length: beyond the end of file")
	ErrCodeNotFound         = errors.New("code not found")
)

func EmitBytes(f io.ReadSeeker, code string) ([]byte, error) {
	count := 1
	isInner := false
	tempContainer := make([]byte, 0, 5)

	var result []byte
	var advancedN uint64 = 1
	var geoDataVarintLength, codeVarintLength, varintLenByteLen uint64 = 0, 0, 0

	// 上界用"文件剩余长度"，是天然正确的约束：条目不可能比文件本身还长。
	// size 在入口量一次，pos 跟着每次读与每次 Seek 的返回值走。
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, ErrFailedToReadBytes
	}
	pos, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return nil, ErrFailedToReadBytes
	}

Loop:
	for {
		remaining := size - pos
		if remaining < 0 {
			// Seek 跳到了文件尾之外（坏长度/坏偏移）。不能让它绕成
			// 一个巨大的 uint64 把下面的上界检查变成空转。
			return nil, ErrInvalidGeodataLength
		}
		// advancedN == 1 的结构性读（字段类型、varint 首字节）不在这里报错：
		// 读到文件尾是"把这个文件扫完了也没找到 code"的正常结局，必须由下面的
		// ReadFull 交出 io.EOF ⇒ ErrCodeNotFound。只有"长度字段声称有 N 字节、
		// 而文件里根本没有"才是要拦的畸形输入 —— 那也正是决定分配多大内存的地方。
		if advancedN > 1 && advancedN > uint64(remaining) {
			return nil, ErrInvalidGeodataLength
		}
		container := make([]byte, advancedN)
		// 必须用 ReadFull：io.Reader 的契约明确允许"短读不算错误"，原来一次 f.Read
		// 读不够就报硬错误，等于把合法的 .dat 交给了实现的心情。
		bytesRead, err := io.ReadFull(f, container)
		pos += int64(bytesRead)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, ErrCodeNotFound
			}
			return nil, ErrFailedToReadExpectedLenBytes
		}

		switch count {
		case 1, 3: // data type ((field_number << 3) | wire_type)
			if container[0] != 10 { // byte `0A` equals to `10` in decimal
				return nil, ErrInvalidGeodataFile
			}
			advancedN = 1
			count++
		case 2, 4: // data length
			tempContainer = append(tempContainer, container...)
			if container[0] > 127 { // max one-byte-length byte `7F`(0FFF FFFF) equals to `127` in decimal
				advancedN = 1
				goto Loop
			}
			lenVarint, n := protowire.ConsumeVarint(tempContainer)
			if n < 0 {
				return nil, ErrInvalidGeodataVarintLength
			}
			tempContainer = nil
			if !isInner {
				isInner = true
				geoDataVarintLength = lenVarint
				advancedN = 1
			} else {
				isInner = false
				codeVarintLength = lenVarint
				varintLenByteLen = uint64(n)
				advancedN = codeVarintLength
			}
			count++
		case 5: // data value
			if strings.EqualFold(string(container), code) {
				count++
				offset := -(1 + int64(varintLenByteLen) + int64(codeVarintLength))
				// Seek 的返回值就是新的绝对位置，必须同步给 pos，
				// 否则下一步的长度上界会用错的位置
				newPos, err := f.Seek(offset, 1) //gosec:disable -- 错误检查在下一行
				if err != nil {
					return nil, ErrFailedToReadBytes
				}
				pos = newPos
				advancedN = geoDataVarintLength // the number of bytes to be read in next round
			} else {
				count = 1
				offset := int64(geoDataVarintLength) - int64(codeVarintLength) - int64(varintLenByteLen) - 1
				newPos, err := f.Seek(offset, 1) // skip the unmatched GeoIP or GeoSite varint //gosec:disable -- 错误检查在下一行
				if err != nil {
					return nil, ErrFailedToReadBytes
				}
				pos = newPos
				advancedN = 1 // the next round will be the start of another GeoIPList or GeoSiteList
			}
		case 6: // matched GeoIP or GeoSite varint
			result = container
			break Loop
		}
	}

	return result, nil
}

func Decode(filename, code string) ([]byte, error) {
	f, err := os.Open(filename) //gosec:disable -- 路径来自配置文件，由用户自己控制，非外部输入
	if err != nil {
		return nil, err
	}
	defer f.Close()

	geoBytes, err := EmitBytes(f, code)
	if err != nil {
		return nil, err
	}
	return geoBytes, nil
}
