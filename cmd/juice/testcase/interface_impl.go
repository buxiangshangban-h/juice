// Code generated by "juice --type Interface --config config.xml --namespace main.UserRepository --output interface_impl.go"; DO NOT EDIT.

package testcase

import (
	"context"
	"database/sql"
	"github.com/eatmoreapple/juice"
	"os/user"
)

type InterfaceImpl struct{}

// GetUserByID 根据用户id查找用户
func (i InterfaceImpl) GetUserByID(ctx context.Context, id int64) (*User, error) {
	manager := juice.ManagerFromContext(ctx)
	var iface Interface = i
	executor := juice.NewGenericManager[*User](manager).Object(iface.GetUserByID)
	return executor.QueryContext(ctx, id)
}

// CreateUser 创建用户
func (i InterfaceImpl) CreateUser(ctx context.Context, u *user.User) error {
	manager := juice.ManagerFromContext(ctx)
	var iface Interface = i
	executor := manager.Object(iface.CreateUser)
	_, err := executor.ExecContext(ctx, u)
	return err
}

// DeleteUserByID 根据id删除用户
func (i InterfaceImpl) DeleteUserByID(ctx context.Context, id int64) (sql.Result, error) {
	manager := juice.ManagerFromContext(ctx)
	var iface Interface = i
	executor := manager.Object(iface.DeleteUserByID)
	return executor.ExecContext(ctx, id)
}

func NewInterface() Interface {
	return &InterfaceImpl{}
}
