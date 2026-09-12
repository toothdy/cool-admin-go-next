package service

import (
	"context"
	"errors"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/gnservice"
	"github.com/toothdy/cool-admin-go-next/modules/dict/dto"
	"github.com/toothdy/cool-admin-go-next/modules/dict/entity"
)

var decimalPattern = regexp.MustCompile(`^[+-]?(?:(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)$`)

type dictTypeRow struct {
	ID  uint64 `orm:"id"`
	Key string `orm:"key"`
}

type dictDataRow struct {
	ID       uint64  `orm:"id"`
	Name     string  `orm:"name"`
	TypeID   uint64  `orm:"typeId"`
	ParentID *uint64 `orm:"parentId"`
	OrderNum int32   `orm:"orderNum"`
	Value    *string `orm:"value"`
}

type dictValueRow struct {
	ID    uint64  `orm:"id"`
	Name  string  `orm:"name"`
	Value *string `orm:"value"`
}

// 字典聚合项
type DataItem struct {
	ID       uint64  `json:"id"`
	Name     string  `json:"name"`
	TypeID   uint64  `json:"typeId"`
	ParentID *uint64 `json:"parentId"`
	OrderNum int32   `json:"orderNum"`
	Value    any     `json:"value"`
}

// 查询字典数据
type InfoService struct {
	*gnservice.Base[entity.Info, uint64]
	typeBase *gnservice.Base[entity.Type, uint64]
}

// 创建字典信息服务
func NewInfo(
	infoBase *gnservice.Base[entity.Info, uint64],
	typeBase *gnservice.Base[entity.Type, uint64],
) *InfoService {

	return &InfoService{Base: infoBase, typeBase: typeBase}
}

// 返回字典数据
func (service *InfoService) Data(ctx context.Context, request *dto.DataRequest) (map[string][]DataItem, error) {
	typeModel, err := service.typeBase.Model(ctx)
	if err != nil {
		return nil, err
	}
	if request != nil && len(request.Types) > 0 {
		typeModel = typeModel.WhereIn("key", request.Types)
	}
	var types []dictTypeRow
	if err = typeModel.Fields("id", "key").Scan(&types); err != nil {
		return nil, exception.WrapCore(err, "查询字典类型失败")
	}
	result := make(map[string][]DataItem, len(types))
	if len(types) == 0 {
		return result, nil
	}
	typeIDs := make([]uint64, len(types))
	for index, current := range types {
		typeIDs[index] = current.ID
	}
	infoModel, err := service.Base.Model(ctx)
	if err != nil {
		return nil, err
	}
	var rows []dictDataRow
	if err = infoModel.
		Fields("id", "name", "typeId", "parentId", "orderNum", "value").
		WhereIn("typeId", typeIDs).
		OrderAsc("orderNum").
		OrderAsc("createTime").
		Scan(&rows); err != nil {
		return nil, exception.WrapCore(err, "查询字典数据失败")
	}
	itemsByType := make(map[uint64][]DataItem, len(types))
	for _, row := range rows {
		itemsByType[row.TypeID] = append(itemsByType[row.TypeID], DataItem{
			ID:       row.ID,
			Name:     row.Name,
			TypeID:   row.TypeID,
			ParentID: row.ParentID,
			OrderNum: row.OrderNum,
			Value:    nodeNumber(row.Value),
		})
	}
	for _, current := range types {
		items := itemsByType[current.ID]
		if items == nil {
			items = []DataItem{}
		}
		result[current.Key] = items
	}

	return result, nil
}

// 返回全部字典类型
func (service *InfoService) Types(ctx context.Context) ([]entity.Type, error) {
	model, err := service.typeBase.Model(ctx)
	if err != nil {
		return nil, err
	}
	var result []entity.Type
	if err = model.Scan(&result); err != nil {
		return nil, exception.WrapCore(err, "查询字典类型失败")
	}

	return result, nil
}

// 返回单个字典名称
func (service *InfoService) GetValue(ctx context.Context, value, key string) (*string, error) {
	rows, exists, err := service.valuesByKey(ctx, key)
	if err != nil || !exists {
		return nil, err
	}

	return findValue(value, rows), nil
}

// 按输入顺序返回多个字典名称
func (service *InfoService) GetValues(ctx context.Context, values []string, key string) ([]*string, error) {
	rows, exists, err := service.valuesByKey(ctx, key)
	if err != nil || !exists {
		return nil, err
	}
	result := make([]*string, len(values))
	for index, value := range values {
		result[index] = findValue(value, rows)
	}

	return result, nil
}

// 删除节点及全部后代
func (service *InfoService) Delete(ctx context.Context, input gnservice.DeleteInput[uint64]) error {
	ids, err := service.descendantIDs(ctx, input.IDs())
	if err != nil {
		return err
	}
	deleteInput, err := gnservice.NewDeleteInput[entity.Info](service.Descriptor(), ids)
	if err != nil {
		return err
	}

	return service.Base.Delete(ctx, deleteInput)
}

func (service *InfoService) valuesByKey(ctx context.Context, key string) ([]dictValueRow, bool, error) {
	typeModel, err := service.typeBase.Model(ctx)
	if err != nil {
		return nil, false, err
	}
	var typeRow *dictTypeRow
	if err = typeModel.Fields("id", "key").Where("key", key).Scan(&typeRow); err != nil {
		return nil, false, exception.WrapCore(err, "查询字典类型失败")
	}
	if typeRow == nil {
		return nil, false, nil
	}
	infoModel, err := service.Base.Model(ctx)
	if err != nil {
		return nil, false, err
	}
	var rows []dictValueRow
	if err = infoModel.Fields("id", "name", "value").Where("typeId", typeRow.ID).Scan(&rows); err != nil {
		return nil, false, exception.WrapCore(err, "查询字典值失败")
	}

	return rows, true, nil
}

func (service *InfoService) descendantIDs(ctx context.Context, roots []uint64) ([]uint64, error) {
	visited := make(map[uint64]struct{}, len(roots))
	result := make([]uint64, 0, len(roots))
	frontier := make([]uint64, 0, len(roots))
	for _, id := range roots {
		if _, exists := visited[id]; exists {
			continue
		}
		visited[id] = struct{}{}
		result = append(result, id)
		frontier = append(frontier, id)
	}
	for len(frontier) > 0 {
		model, err := service.Base.Model(ctx)
		if err != nil {
			return nil, err
		}
		var rows []struct {
			ID uint64 `orm:"id"`
		}
		if err = model.Fields("id").WhereIn("parentId", frontier).Scan(&rows); err != nil {
			return nil, exception.WrapCore(err, "查询子字典失败")
		}
		frontier = frontier[:0]
		for _, row := range rows {
			if _, exists := visited[row.ID]; exists {
				continue
			}
			visited[row.ID] = struct{}{}
			result = append(result, row.ID)
			frontier = append(frontier, row.ID)
		}
	}

	return result, nil
}

func findValue(value string, rows []dictValueRow) *string {
	for _, row := range rows {
		if row.Value != nil && *row.Value == value {
			name := row.Name
			return &name
		}
	}
	id, exists := nodeParsedInt(value)
	if !exists {
		return nil
	}
	for _, row := range rows {
		if float64(row.ID) == id {
			name := row.Name
			return &name
		}
	}

	return nil
}

func nodeNumber(value *string) any {
	if value == nil {
		return nil
	}
	raw := *value
	if raw == "" {
		return raw
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return float64(0)
	}
	if trimmed == "Infinity" || trimmed == "+Infinity" || trimmed == "-Infinity" {
		return nil
	}
	if number, exists := nodeBaseNumber(trimmed); exists {
		return number
	}
	if !decimalPattern.MatchString(trimmed) {
		return raw
	}
	number, err := strconv.ParseFloat(trimmed, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return raw
	}
	if math.IsInf(number, 0) {
		return nil
	}
	if number == 0 {
		return float64(0)
	}

	return number
}

func nodeBaseNumber(value string) (any, bool) {
	if len(value) < 3 || value[0] != '0' {
		return nil, false
	}
	base := 0
	switch value[1] {
	case 'b', 'B':
		base = 2
	case 'o', 'O':
		base = 8
	case 'x', 'X':
		base = 16
	default:
		return nil, false
	}
	integer, exists := new(big.Int).SetString(value[2:], base)
	if !exists {
		return nil, false
	}
	number, _ := new(big.Float).SetInt(integer).Float64()
	if math.IsInf(number, 0) {
		return nil, true
	}

	return number, true
}

func nodeParsedInt(value string) (float64, bool) {
	value = strings.TrimLeftFunc(value, unicode.IsSpace)
	if value == "" {
		return 0, false
	}
	sign := 1
	if value[0] == '+' || value[0] == '-' {
		if value[0] == '-' {
			sign = -1
		}
		value = value[1:]
	}
	base := 10
	if len(value) >= 2 && value[0] == '0' && (value[1] == 'x' || value[1] == 'X') {
		base = 16
		value = value[2:]
	}
	end := 0
	for end < len(value) && validDigit(value[end], base) {
		end++
	}
	if end == 0 {
		return 0, false
	}
	integer, exists := new(big.Int).SetString(value[:end], base)
	if !exists {
		return 0, false
	}
	number, _ := new(big.Float).SetInt(integer).Float64()
	number *= float64(sign)
	if math.IsInf(number, 0) || number < 0 {
		return 0, false
	}

	return number, true
}

func validDigit(value byte, base int) bool {
	if value >= '0' && value <= '9' {
		return int(value-'0') < base
	}
	if base == 16 && value >= 'a' && value <= 'f' {
		return true
	}
	return base == 16 && value >= 'A' && value <= 'F'
}
