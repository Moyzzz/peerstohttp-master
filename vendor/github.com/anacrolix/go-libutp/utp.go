package utp

/*
#cgo CPPFLAGS: -DPOSIX -DUTP_DEBUG_LOGGING=0
#cgo CFLAGS: -Wall -O3
// These are all copied from the libutp Makefile.
#cgo CXXFLAGS: -Wall -O3 -fPIC -Wno-sign-compare
// There are some variables that aren't used unless UTP_DEBUG_LOGGING is defined.
#cgo CXXFLAGS: -Wno-unused-const-variable
// Windows additional flags
#cgo windows LDFLAGS: -lws2_32
#cgo windows CXXFLAGS: -D_WIN32_WINNT=0x600
#include "utp.h"

uint64_t firewallCallback(utp_callback_arguments *);
uint64_t errorCallback(utp_callback_arguments *);
uint64_t logCallback(utp_callback_arguments *);
uint64_t acceptCallback(utp_callback_arguments *);
uint64_t sendtoCallback(utp_callback_arguments *);
uint64_t stateChangeCallback(utp_callback_arguments *);
uint64_t readCallback(utp_callback_arguments *);
uint64_t getReadBufferSizeCallback(utp_callback_arguments *);
*/
import "C"
import "unsafe"

type socklen C.socklen_t

// 注：Go 1.21+ 禁止在 cgo 生成的不完整类型（C.utp_context 为 typedef 别名）
// 上定义方法，故改为普通函数，调用点同步调整。

func setCallbacks(ctx *C.utp_context) {
	C.utp_set_callback(ctx, C.UTP_ON_FIREWALL, (*C.utp_callback_t)(C.firewallCallback))
	C.utp_set_callback(ctx, C.UTP_LOG, (*C.utp_callback_t)(C.logCallback))
	C.utp_set_callback(ctx, C.UTP_ON_ACCEPT, (*C.utp_callback_t)(C.acceptCallback))
	C.utp_set_callback(ctx, C.UTP_SENDTO, (*C.utp_callback_t)(C.sendtoCallback))
	C.utp_set_callback(ctx, C.UTP_ON_STATE_CHANGE, (*C.utp_callback_t)(C.stateChangeCallback))
	C.utp_set_callback(ctx, C.UTP_ON_READ, (*C.utp_callback_t)(C.readCallback))
	C.utp_set_callback(ctx, C.UTP_ON_ERROR, (*C.utp_callback_t)(C.errorCallback))
	C.utp_set_callback(ctx, C.UTP_GET_READ_BUFFER_SIZE, (*C.utp_callback_t)(C.getReadBufferSizeCallback))
}

func setOption(ctx *C.utp_context, opt Option, val int) int {
	return int(C.utp_context_set_option(ctx, opt, C.int(val)))
}

func libStateName(state C.int) string {
	return C.GoString((*[5]*C.char)(unsafe.Pointer(&C.utp_state_names))[state])
}

func libErrorCodeNames(error_code C.int) string {
	return C.GoString((*[3]*C.char)(unsafe.Pointer(&C.utp_error_code_names))[error_code])
}
