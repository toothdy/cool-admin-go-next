package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/modules/task/entity"
)

const minIntervalMs = 1000 // 间隔任务的最小粒度

const nextRunTimeHorizon = 366 * 24 * time.Hour // 前向扫描下次执行时间的上限

// gcron 六段模式的段序
const (
	fieldSecond = iota
	fieldMinute
	fieldHour
	fieldDay
	fieldMonth
	fieldWeek
	fieldCount
)

var patternFieldRange = [fieldCount][2]int{
	fieldSecond: {0, 59},
	fieldMinute: {0, 59},
	fieldHour:   {0, 23},
	fieldDay:    {1, 31},
	fieldMonth:  {1, 12},
	fieldWeek:   {0, 6},
}

// 任务定时规则
type Schedule struct {
	pattern      string
	everySeconds int64
	fields       *[fieldCount]map[int]bool
}

// 把任务定时配置编译为 gcron 模式
func CompileSchedule(taskType int32, cronExpression *string, every *int64) (*Schedule, error) {
	if taskType == entity.TaskTypeInterval {
		if every == nil || *every < minIntervalMs {
			return nil, exception.Validate("任务间隔不能小于 1000 毫秒")
		}
		seconds := *every / minIntervalMs

		return &Schedule{pattern: fmt.Sprintf("@every %ds", seconds), everySeconds: seconds}, nil
	}
	if cronExpression == nil || strings.TrimSpace(*cronExpression) == "" {
		return nil, exception.Validate("cron 不能为空")
	}
	segments := strings.Fields(*cronExpression)
	switch len(segments) {
	case fieldCount:
	case fieldCount - 1:
		segments = append([]string{"0"}, segments...)
	default:
		return nil, exception.Validate("cron 必须是 5 段或 6 段表达式")
	}

	return &Schedule{pattern: strings.Join(segments, " "), fields: parsePatternFields(segments)}, nil
}

// gcron 可注册的模式
func (schedule *Schedule) Pattern() string {
	if schedule == nil {
		return ""
	}

	return schedule.pattern
}

// 晚于给定时刻的下次执行时间
func (schedule *Schedule) Next(after time.Time) (time.Time, bool) {
	if schedule == nil {
		return time.Time{}, false
	}
	if schedule.everySeconds > 0 {
		return after.Add(time.Duration(schedule.everySeconds) * time.Second), true
	}
	if schedule.fields == nil {
		return time.Time{}, false
	}
	fields := *schedule.fields
	deadline := after.Add(nextRunTimeHorizon)
	current := after.Truncate(time.Second).Add(time.Second)
	for current.Before(deadline) {
		if !fields[fieldMonth][int(current.Month())] {
			current = time.Date(current.Year(), current.Month(), 1, 0, 0, 0, 0, current.Location()).AddDate(0, 1, 0)
			continue
		}
		if !fields[fieldDay][current.Day()] || !fields[fieldWeek][int(current.Weekday())] {
			current = time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, current.Location()).AddDate(0, 0, 1)
			continue
		}
		if !fields[fieldHour][current.Hour()] {
			current = current.Truncate(time.Hour).Add(time.Hour)
			continue
		}
		if !fields[fieldMinute][current.Minute()] {
			current = current.Truncate(time.Minute).Add(time.Minute)
			continue
		}
		if !fields[fieldSecond][current.Second()] {
			current = current.Add(time.Second)
			continue
		}

		return current, true
	}

	return time.Time{}, false
}

// 解析六段任务模式
func parsePatternFields(segments []string) *[fieldCount]map[int]bool {
	var fields [fieldCount]map[int]bool
	for index, segment := range segments {
		values, valid := parsePatternField(segment, patternFieldRange[index][0], patternFieldRange[index][1])
		if !valid {
			return nil
		}
		fields[index] = values
	}

	return &fields
}

func parsePatternField(segment string, minimum, maximum int) (map[int]bool, bool) {
	values := make(map[int]bool, maximum-minimum+1)
	if segment == "*" || segment == "?" {
		for value := minimum; value <= maximum; value++ {
			values[value] = true
		}

		return values, true
	}
	for _, element := range strings.Split(segment, ",") {
		bounds, step, valid := parsePatternElement(element, minimum, maximum)
		if !valid {
			return nil, false
		}
		for value := bounds[0]; value <= bounds[1]; value += step {
			values[value] = true
		}
	}

	return values, len(values) > 0
}

func parsePatternElement(element string, minimum, maximum int) ([2]int, int, bool) {
	step := 1
	interval := strings.Split(element, "/")
	if len(interval) == 2 {
		parsed, err := strconv.Atoi(interval[1])
		if err != nil || parsed <= 0 {
			return [2]int{}, 0, false
		}
		step = parsed
	}
	if len(interval) > 2 {
		return [2]int{}, 0, false
	}
	bounds := [2]int{minimum, maximum}
	span := strings.Split(interval[0], "-")
	if len(span) > 2 {
		return [2]int{}, 0, false
	}
	if span[0] != "*" {
		parsed, err := strconv.Atoi(span[0])
		if err != nil {
			return [2]int{}, 0, false
		}
		bounds[0] = parsed
		if len(interval) == 1 && len(span) == 1 {
			bounds[1] = parsed
		}
	}
	if len(span) == 2 {
		parsed, err := strconv.Atoi(span[1])
		if err != nil {
			return [2]int{}, 0, false
		}
		bounds[1] = parsed
	}
	if bounds[0] < minimum || bounds[1] > maximum || bounds[0] > bounds[1] {
		return [2]int{}, 0, false
	}

	return bounds, step, true
}
