// Пакет httpx — строгий разбор JSON-запросов и единый формат ответов (спецификация §20).
package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"teyljunior/internal/core"
)

// MaxJSONDepth ограничивает вложенность при проверке повторных ключей.
const MaxJSONDepth = 32

// Decode строго разбирает тело в v и возвращает каноническую форму тела для хеша
// идемпотентности. Отклоняются: невалидный UTF-8, повторные ключи на любом уровне, лишние поля,
// несколько JSON-значений подряд, нецелые или слишком большие числа там, где ожидается целое.
// Стандартный DisallowUnknownFields повторные ключи не ловит (§20), поэтому сначала идёт
// отдельный проход по токенам.
func Decode(body []byte, v any) (canonical []byte, err error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, invalidJSON("пустое тело запроса")
	}
	if !utf8.Valid(body) {
		return nil, invalidJSON("тело запроса не является корректным UTF-8")
	}
	tree, err := parseTree(body)
	if err != nil {
		return nil, err
	}
	if _, ok := tree.(map[string]any); !ok {
		return nil, invalidJSON("ожидался JSON-объект")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return nil, decodeProblem(err)
	}
	// Канонический вид: ключи отсортированы, пробелы убраны, числа сохранены как записаны.
	canonical, err = json.Marshal(tree)
	if err != nil {
		return nil, invalidJSON("не удалось нормализовать запрос")
	}
	return canonical, nil
}

// parseTree разбирает ровно одно JSON-значение, проверяя повторные ключи.
func parseTree(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	v, err := parseValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, invalidJSON("после JSON-значения есть лишние данные")
	}
	return v, nil
}

func parseValue(dec *json.Decoder, depth int) (any, error) {
	if depth > MaxJSONDepth {
		return nil, invalidJSON("слишком глубокая вложенность")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, invalidJSON("некорректный JSON")
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := map[string]any{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, invalidJSON("некорректный JSON")
				}
				key := kt.(string)
				if _, dup := obj[key]; dup {
					return nil, invalidJSON(fmt.Sprintf("ключ %q повторяется", key))
				}
				val, err := parseValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				obj[key] = val
			}
			if _, err := dec.Token(); err != nil {
				return nil, invalidJSON("некорректный JSON")
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := parseValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, invalidJSON("некорректный JSON")
			}
			return arr, nil
		}
		return nil, invalidJSON("некорректный JSON")
	case json.Number:
		f, err := t.Float64()
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, invalidJSON("некорректное число")
		}
		return t, nil
	default:
		return t, nil
	}
}

func invalidJSON(msg string) *core.Error { return core.BadRequest("INVALID_JSON", msg) }

// decodeProblem описывает ошибку типизированного разбора без эха входных данных.
// Структурная ошибка (неизвестное поле, неверный тип) — 422, синтаксис — 400.
func decodeProblem(err error) *core.Error {
	if te, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return core.Validation(te.Field, "неверный тип значения")
	}
	if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return core.Validation(strings.Trim(field, `"`), "неизвестное поле")
	}
	return invalidJSON("некорректный JSON")
}
