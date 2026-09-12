package protocol

const (
	Name                    = "cool.plugin/v1"
	Version          uint32 = 1
	HostModule              = "cool_host"
	MemoryExport            = "memory"
	InitializeExport        = "_initialize"
)

const (
	ExportABIVersion      = "cool_abi_version"
	ExportAlloc           = "cool_alloc"
	ExportFree            = "cool_free"
	ExportInit            = "cool_init"
	ExportInvoke          = "cool_invoke"
	ExportShutdown        = "cool_shutdown"
	ExportResponsePointer = "cool_response_pointer"
	ExportResponseLength  = "cool_response_length"
	ExportResponseDrop    = "cool_response_drop"
)

const (
	ImportCall           = "call"
	ImportResponseLength = "response_length"
	ImportResponseRead   = "response_read"
	ImportResponseDrop   = "response_drop"
)

// WebAssembly 数值类型
type ValueType byte

const (
	ValueTypeI32 ValueType = 0x7f
	ValueTypeI64 ValueType = 0x7e
)

// ABI 函数签名
type FunctionSignature struct {
	Name       string
	Parameters []ValueType
	Results    []ValueType
}

var guestExports = []FunctionSignature{
	{Name: ExportABIVersion, Results: []ValueType{ValueTypeI32}},
	{Name: ExportAlloc, Parameters: []ValueType{ValueTypeI32}, Results: []ValueType{ValueTypeI32}},
	{Name: ExportFree, Parameters: []ValueType{ValueTypeI32, ValueTypeI32}},
	{Name: ExportInit, Parameters: []ValueType{ValueTypeI64, ValueTypeI32, ValueTypeI32}, Results: []ValueType{ValueTypeI64}},
	{Name: ExportInvoke, Parameters: []ValueType{ValueTypeI64, ValueTypeI32, ValueTypeI32, ValueTypeI32, ValueTypeI32}, Results: []ValueType{ValueTypeI64}},
	{Name: ExportShutdown, Parameters: []ValueType{ValueTypeI64}, Results: []ValueType{ValueTypeI64}},
	{Name: ExportResponsePointer, Parameters: []ValueType{ValueTypeI64}, Results: []ValueType{ValueTypeI32}},
	{Name: ExportResponseLength, Parameters: []ValueType{ValueTypeI64}, Results: []ValueType{ValueTypeI32}},
	{Name: ExportResponseDrop, Parameters: []ValueType{ValueTypeI64}},
}

var hostImports = []FunctionSignature{
	{Name: ImportCall, Parameters: []ValueType{ValueTypeI64, ValueTypeI32, ValueTypeI32, ValueTypeI32, ValueTypeI32}, Results: []ValueType{ValueTypeI64}},
	{Name: ImportResponseLength, Parameters: []ValueType{ValueTypeI64}, Results: []ValueType{ValueTypeI32}},
	{Name: ImportResponseRead, Parameters: []ValueType{ValueTypeI64, ValueTypeI32, ValueTypeI32}, Results: []ValueType{ValueTypeI32}},
	{Name: ImportResponseDrop, Parameters: []ValueType{ValueTypeI64}},
}

// 返回 Guest 必需导出签名
func GuestExports() []FunctionSignature {
	return cloneSignatures(guestExports)
}

// 返回 Host 必需导入签名
func HostImports() []FunctionSignature {
	return cloneSignatures(hostImports)
}

func cloneSignatures(input []FunctionSignature) []FunctionSignature {
	result := make([]FunctionSignature, len(input))
	for index, signature := range input {
		result[index] = FunctionSignature{
			Name:       signature.Name,
			Parameters: append([]ValueType(nil), signature.Parameters...),
			Results:    append([]ValueType(nil), signature.Results...),
		}
	}

	return result
}
