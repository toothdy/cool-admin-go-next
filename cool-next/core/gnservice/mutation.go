package gnservice

import (
	"context"

	"fmt"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/crud"
	"github.com/toothdy/cool-admin-go-next/cool-next/db/tx"
)

// CRUD 动作的 Service 别名
type Action = crud.Action

// 生成期选定的 Service 动作模式
type ActionMode = crud.ActionMode

const (
	ActionModeBase     = crud.ActionModeBase     // 基础实现
	ActionModeOverride = crud.ActionModeOverride // 完全覆盖
	ActionModeDelegate = crud.ActionModeDelegate // 委托基础实现
)

// 修改前 Hook
type ModifyBeforeHook[E any, ID comparable] interface {
	ModifyBefore(context.Context, *Mutation[E, ID]) error
}

// 修改后 Hook
type ModifyAfterHook[E any, ID comparable] interface {
	ModifyAfter(context.Context, *Mutation[E, ID]) error
}

// 单次整批写操作
type Mutation[E any, ID comparable] struct {
	action      Action
	addInput    AddInput[E]
	deleteInput DeleteInput[ID]
	resultIDs   []ID
	updateInput UpdateInput[E, ID]
}

// 构造新增 Mutation
func NewAddMutation[E any, ID comparable](input AddInput[E]) (*Mutation[E, ID], error) {
	if err := validateAddInput(input); err != nil {
		return nil, err
	}

	return &Mutation[E, ID]{action: crud.ActionAdd, addInput: input}, nil
}

// 构造删除 Mutation
func NewDeleteMutation[E any, ID comparable](input DeleteInput[ID]) (*Mutation[E, ID], error) {
	if len(input.ids) == 0 {
		return nil, exception.Validate("删除 Mutation 的 ID 不能为空")
	}

	return &Mutation[E, ID]{action: crud.ActionDelete, deleteInput: input}, nil
}

// 构造更新 Mutation
func NewUpdateMutation[E any, ID comparable](input UpdateInput[E, ID]) (*Mutation[E, ID], error) {
	if err := validateUpdateInput(input); err != nil {
		return nil, err
	}

	return &Mutation[E, ID]{action: crud.ActionUpdate, updateInput: input}, nil
}

// 返回 CRUD 动作
func (mutation *Mutation[E, ID]) Action() Action {
	if mutation == nil {
		return ""
	}

	return mutation.action
}

// 返回新增输入
func (mutation *Mutation[E, ID]) AddInput() AddInput[E] {
	if mutation == nil || mutation.action != crud.ActionAdd {
		return AddInput[E]{}
	}

	return mutation.addInput
}

// 返回更新输入
func (mutation *Mutation[E, ID]) UpdateInput() UpdateInput[E, ID] {
	if mutation == nil || mutation.action != crud.ActionUpdate {
		return UpdateInput[E, ID]{}
	}

	return mutation.updateInput
}

// 返回删除 ID
func (mutation *Mutation[E, ID]) DeleteIDs() []ID {
	if mutation == nil || mutation.action != crud.ActionDelete {
		return nil
	}

	return mutation.deleteInput.IDs()
}

// 返回新增结果 ID
func (mutation *Mutation[E, ID]) ResultIDs() []ID {
	if mutation == nil {
		return nil
	}

	return append([]ID(nil), mutation.resultIDs...)
}

// 在当前事务中执行整批 Hook 和写操作
func ExecuteMutation[E any, ID comparable](
	ctx context.Context,
	mutation *Mutation[E, ID],
	before ModifyBeforeHook[E, ID],
	after ModifyAfterHook[E, ID],
	modify func(context.Context) (AddResult[ID], error),
) (AddResult[ID], error) {
	if ctx == nil {
		return AddResult[ID]{}, exception.Core("Mutation 上下文不能为空")
	}
	if err := validateMutation(mutation); err != nil {
		return AddResult[ID]{}, err
	}
	transaction, group, exists := tx.Current(ctx)
	if !exists || transaction == nil || group == "" {
		return AddResult[ID]{}, exception.Core("Mutation 必须在框架事务中执行")
	}
	dispatch, exists := crud.CurrentDispatch(ctx)
	if !exists {
		return AddResult[ID]{}, exception.Core("Mutation 必须由 CRUD Dispatcher 调用")
	}
	if dispatch.Action() != mutation.action {
		return AddResult[ID]{}, exception.Core(fmt.Sprintf("CRUD 调度动作不匹配: 当前 %s，请求 %s",
			dispatch.Action(),
			mutation.action),
		)
	}
	operation, hasOperation := crud.CurrentOperation(ctx)
	switch dispatch.Mode() {
	case crud.ActionModeBase, crud.ActionModeDelegate, crud.ActionModeOverride:
	default:
		return AddResult[ID]{}, exception.Core("CRUD 调度模式无效")
	}
	if !hasOperation || operation.Plan() == nil {
		return AddResult[ID]{}, exception.Core("当前上下文不存在 CRUD 动作计划")
	}
	if operation.Plan().Action() != mutation.action {
		return AddResult[ID]{}, exception.Core(fmt.Sprintf("CRUD 动作计划不匹配: 当前 %s，请求 %s",
			operation.Plan().Action(),
			mutation.action),
		)
	}
	if modify == nil {
		return AddResult[ID]{}, exception.Core("Mutation 写操作不能为空")
	}
	if !isNil(before) {
		if err := before.ModifyBefore(ctx, mutation); err != nil {
			return AddResult[ID]{}, err
		}
	}
	result, err := modify(ctx)
	if err != nil {
		return AddResult[ID]{}, err
	}
	if mutation.action == crud.ActionAdd {
		if err = mutation.setAddResult(result); err != nil {
			return AddResult[ID]{}, err
		}
	}
	if !isNil(after) {
		if err = after.ModifyAfter(ctx, mutation); err != nil {
			return AddResult[ID]{}, err
		}
	}

	return result, nil
}

func validateAddInput[E any](input AddInput[E]) error {
	if input.isMany {
		if input.one != nil || len(input.many) == 0 {
			return exception.Validate("新增 Mutation 输入无效")
		}
		for _, value := range input.many {
			if value == nil || value.descriptor == nil {
				return exception.Validate("新增 Mutation 输入无效")
			}
		}

		return nil
	}
	if input.one == nil || input.one.descriptor == nil || len(input.many) != 0 {
		return exception.Validate("新增 Mutation 输入无效")
	}

	return nil
}

func validateUpdateInput[E any, ID comparable](input UpdateInput[E, ID]) error {
	if input.isMany {
		if input.one.mutable != nil || len(input.many) == 0 {
			return exception.Validate("更新 Mutation 输入无效")
		}
		for _, item := range input.many {
			if item.mutable == nil || item.mutable.descriptor == nil {
				return exception.Validate("更新 Mutation 输入无效")
			}
		}

		return nil
	}
	if input.one.mutable == nil || input.one.mutable.descriptor == nil || len(input.many) != 0 {
		return exception.Validate("更新 Mutation 输入无效")
	}

	return nil
}

func validateMutation[E any, ID comparable](mutation *Mutation[E, ID]) error {
	if mutation == nil {
		return exception.Validate("Mutation 不能为空")
	}
	switch mutation.action {
	case crud.ActionAdd:
		return validateAddInput(mutation.addInput)
	case crud.ActionDelete:
		if len(mutation.deleteInput.ids) == 0 {
			return exception.Validate("删除 Mutation 的 ID 不能为空")
		}
	case crud.ActionUpdate:
		return validateUpdateInput(mutation.updateInput)
	default:
		return exception.Validate("Mutation 动作无效")
	}

	return nil
}

func (mutation *Mutation[E, ID]) setAddResult(result AddResult[ID]) error {
	if result.isMany != mutation.addInput.isMany {
		return exception.Core("新增结果形状与输入不匹配")
	}
	if result.isMany {
		if len(result.many) != len(mutation.addInput.many) {
			return exception.Core("新增结果数量与输入不匹配")
		}
		mutation.resultIDs = result.Many()

		return nil
	}
	mutation.resultIDs = []ID{result.one}

	return nil
}
