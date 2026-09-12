package seed

// cool generate 从模块根嵌入的种子数据
type Data struct {
	db   []byte
	menu []byte
}

// 由生成代码调用，构造模块种子数据
func NewData(db, menu []byte) Data {
	return Data{db: db, menu: menu}
}

// db.json 内容副本
func (data Data) DB() []byte {
	return cloneBytes(data.db)
}

// menu.json 内容副本
func (data Data) Menu() []byte {
	return cloneBytes(data.menu)
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}

	return append([]byte(nil), value...)
}
