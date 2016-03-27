// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bpf

import "fmt"

// An Instruction is one instruction executed by the BPF virtual
// machine.
type Instruction interface {
	// Assemble assembles the Instruction into a RawInstruction.
	Assemble() (RawInstruction, error)
}

// A RawInstruction is a raw BPF virtual machine instruction.
type RawInstruction struct {
	// Operation to execute.
	Op uint16
	// For conditional jump instructions, the number of instructions
	// to skip if the condition is true/false.
	Jt uint8
	Jf uint8
	// Constant parameter. The meaning depends on the Op.
	K uint32
}

// Assemble implements the Instruction Assemble method.
func (ri RawInstruction) Assemble() (RawInstruction, error) { return ri, nil }

// Disassemble parses ri into an Instruction and returns it. If ri is
// not recognized by this package, ri itself is returned.
func (ri RawInstruction) Disassemble() Instruction {
	switch ri.Op {
	case opClsLoadA | opLoadWidth4 | opAddrModeImmediate:
		return LoadConstant{Dst: RegA, Val: ri.K}
	case opClsLoadX | opLoadWidth4 | opAddrModeImmediate:
		return LoadConstant{Dst: RegX, Val: ri.K}

	case opClsLoadA | opLoadWidth4 | opAddrModeScratch:
		if ri.K > 15 {
			return ri
		}
		return LoadScratch{Dst: RegA, N: int(ri.K)}
	case opClsLoadX | opLoadWidth4 | opAddrModeScratch:
		if ri.K > 15 {
			return ri
		}
		return LoadScratch{Dst: RegX, N: int(ri.K)}

	case opClsLoadA | opLoadWidth4 | opAddrModeAbsolute:
		ext := Extension(uint32(ri.K) + 0x1000)
		switch ext {
		case ExtProto, ExtType, ExtPayloadOffset, ExtInterfaceIndex, ExtNetlinkAttr, ExtNetlinkAttrNested, ExtMark, ExtQueue, ExtLinkLayerType, ExtRXHash, ExtCPUID, ExtVLANTag, ExtVLANTagPresent, ExtVLANProto, ExtRand:
			return LoadExtension{Num: ext}
		default:
			return LoadAbsolute{Off: ri.K, Size: 4}
		}
	case opClsLoadA | opLoadWidth2 | opAddrModeAbsolute:
		return LoadAbsolute{Off: ri.K, Size: 2}
	case opClsLoadA | opLoadWidth1 | opAddrModeAbsolute:
		return LoadAbsolute{Off: ri.K, Size: 1}

	case opClsLoadA | opLoadWidth4 | opAddrModeIndirect:
		return LoadIndirect{Off: ri.K, Size: 4}
	case opClsLoadA | opLoadWidth2 | opAddrModeIndirect:
		return LoadIndirect{Off: ri.K, Size: 2}
	case opClsLoadA | opLoadWidth1 | opAddrModeIndirect:
		return LoadIndirect{Off: ri.K, Size: 1}

	case opClsLoadX | opLoadWidth1 | opAddrModeIPv4HeaderLen:
		return LoadIPv4HeaderLen{Off: ri.K}

	case opClsLoadA | opLoadWidth4 | opAddrModePacketLen:
		return LoadExtension{Num: ExtLen}

	case opClsStoreA:
		if ri.K > 15 {
			return ri
		}
		return StoreScratch{Src: RegA, N: int(ri.K)}
	case opClsStoreX:
		if ri.K > 15 {
			return ri
		}
		return StoreScratch{Src: RegX, N: int(ri.K)}

	case opClsALU | uint16(aluOpNeg):
		return NegateA{}

	case opClsJump | opJumpAlways:
		return Jump{Skip: ri.K}
	case opClsJump | opJumpEqual:
		return JumpIf{
			Cond:      JumpEqual,
			Val:       ri.K,
			SkipTrue:  ri.Jt,
			SkipFalse: ri.Jf,
		}
	case opClsJump | opJumpGT:
		return JumpIf{
			Cond:      JumpGreaterThan,
			Val:       ri.K,
			SkipTrue:  ri.Jt,
			SkipFalse: ri.Jf,
		}
	case opClsJump | opJumpGE:
		return JumpIf{
			Cond:      JumpGreaterOrEqual,
			Val:       ri.K,
			SkipTrue:  ri.Jt,
			SkipFalse: ri.Jf,
		}
	case opClsJump | opJumpSet:
		return JumpIf{
			Cond:      JumpBitsSet,
			Val:       ri.K,
			SkipTrue:  ri.Jt,
			SkipFalse: ri.Jf,
		}

	case opClsReturn | opRetSrcA:
		return RetA{}
	case opClsReturn | opRetSrcConstant:
		return RetConstant{Val: ri.K}

	case opClsMisc | opMiscTXA:
		return TXA{}
	case opClsMisc | opMiscTAX:
		return TAX{}
	}

	// ALU operations require bitmasking to decode, so are done
	// outside the main switch.

	if ri.Op&opClsMask != opClsALU {
		return ri
	}

	op := ALUOp(ri.Op & opALUOpMask)
	switch op {
	case ALUOpAdd, ALUOpSub, ALUOpMul, ALUOpDiv, ALUOpOr, ALUOpAnd, ALUOpShiftLeft, ALUOpShiftRight, ALUOpMod, ALUOpXor:
	default:
		return ri
	}
	if ri.Op&opALUSrcMask != 0 {
		return ALUOpX{Op: op}
	}
	return ALUOpConstant{Op: op, Val: ri.K}
}

// LoadConstant loads Val into register Dst.
type LoadConstant struct {
	Dst Register
	Val uint32
}

// Assemble implements the Instruction Assemble method.
func (a LoadConstant) Assemble() (RawInstruction, error) {
	return assembleLoad(a.Dst, 4, opAddrModeImmediate, a.Val)
}

// LoadScratch loads scratch[N] into register Dst.
type LoadScratch struct {
	Dst Register
	N   int // 0-15
}

// Assemble implements the Instruction Assemble method.
func (a LoadScratch) Assemble() (RawInstruction, error) {
	if a.N < 0 || a.N > 15 {
		return RawInstruction{}, fmt.Errorf("invalid scratch slot %d", a.N)
	}
	return assembleLoad(a.Dst, 4, opAddrModeScratch, uint32(a.N))
}

// LoadAbsolute loads packet[Off:Off+Size] as an integer value into
// register A.
type LoadAbsolute struct {
	Off  uint32
	Size int // 1, 2 or 4
}

// Assemble implements the Instruction Assemble method.
func (a LoadAbsolute) Assemble() (RawInstruction, error) {
	return assembleLoad(RegA, a.Size, opAddrModeAbsolute, a.Off)
}

// LoadIndirect loads packet[X+Off:X+Off+Size] as an integer value
// into register A.
type LoadIndirect struct {
	Off  uint32
	Size int // 1, 2 or 4
}

// Assemble implements the Instruction Assemble method.
func (a LoadIndirect) Assemble() (RawInstruction, error) {
	return assembleLoad(RegA, a.Size, opAddrModeIndirect, a.Off)
}

// LoadIPv4HeaderLen loads into register X the length of the IPv4
// header whose first byte is packet[Off].
type LoadIPv4HeaderLen struct {
	Off uint32
}

// Assemble implements the Instruction Assemble method.
func (a LoadIPv4HeaderLen) Assemble() (RawInstruction, error) {
	return assembleLoad(RegX, 1, opAddrModeIPv4HeaderLen, a.Off)
}

// LoadExtension invokes a linux-specific extension and stores the
// result in register A.
type LoadExtension struct {
	Num Extension
}

// Assemble implements the Instruction Assemble method.
func (a LoadExtension) Assemble() (RawInstruction, error) {
	if a.Num == ExtLen {
		return assembleLoad(RegA, 4, opAddrModePacketLen, 0)
	}
	return assembleLoad(RegA, 4, opAddrModeAbsolute, uint32(-0x1000+a.Num))
}

// StoreScratch stores register Src into scratch[N].
type StoreScratch struct {
	Src Register
	N   int // 0-15
}

// Assemble implements the Instruction Assemble method.
func (a StoreScratch) Assemble() (RawInstruction, error) {
	if a.N < 0 || a.N > 15 {
		return RawInstruction{}, fmt.Errorf("invalid scratch slot %d", a.N)
	}
	var op uint16
	switch a.Src {
	case RegA:
		op = opClsStoreA
	case RegX:
		op = opClsStoreX
	default:
		return RawInstruction{}, fmt.Errorf("invalid source register %v", a.Src)
	}

	return RawInstruction{
		Op: op,
		K:  uint32(a.N),
	}, nil
}

// ALUOpConstant executes A = A <Op> Val.
type ALUOpConstant struct {
	Op  ALUOp
	Val uint32
}

// Assemble implements the Instruction Assemble method.
func (a ALUOpConstant) Assemble() (RawInstruction, error) {
	return RawInstruction{
		Op: opClsALU | opALUSrcConstant | uint16(a.Op),
		K:  a.Val,
	}, nil
}

// ALUOpX executes A = A <Op> X
type ALUOpX struct {
	Op ALUOp
}

// Assemble implements the Instruction Assemble method.
func (a ALUOpX) Assemble() (RawInstruction, error) {
	return RawInstruction{
		Op: opClsALU | opALUSrcX | uint16(a.Op),
	}, nil
}

// NegateA executes A = -A.
type NegateA struct{}

// Assemble implements the Instruction Assemble method.
func (a NegateA) Assemble() (RawInstruction, error) {
	return RawInstruction{
		Op: opClsALU | uint16(aluOpNeg),
	}, nil
}

// Jump skips the following Skip instructions in the program.
type Jump struct {
	Skip uint32
}

// Assemble implements the Instruction Assemble method.
func (a Jump) Assemble() (RawInstruction, error) {
	return RawInstruction{
		Op: opClsJump | opJumpAlways,
		K:  a.Skip,
	}, nil
}

// JumpIf skips the following Skip instructions in the program if A
// <Cond> Val is true.
type JumpIf struct {
	Cond      JumpTest
	Val       uint32
	SkipTrue  uint8
	SkipFalse uint8
}

// Assemble implements the Instruction Assemble method.
func (a JumpIf) Assemble() (RawInstruction, error) {
	var (
		cond uint16
		flip bool
	)
	switch a.Cond {
	case JumpEqual:
		cond = opJumpEqual
	case JumpNotEqual:
		cond, flip = opJumpEqual, true
	case JumpGreaterThan:
		cond = opJumpGT
	case JumpLessThan:
		cond, flip = opJumpGE, true
	case JumpGreaterOrEqual:
		cond = opJumpGE
	case JumpLessOrEqual:
		cond, flip = opJumpGT, true
	case JumpBitsSet:
		cond = opJumpSet
	case JumpBitsNotSet:
		cond, flip = opJumpSet, true
	default:
		return RawInstruction{}, fmt.Errorf("unknown JumpTest %v", a.Cond)
	}
	jt, jf := a.SkipTrue, a.SkipFalse
	if flip {
		jt, jf = jf, jt
	}
	return RawInstruction{
		Op: opClsJump | cond,
		Jt: jt,
		Jf: jf,
		K:  a.Val,
	}, nil
}

// RetA exits the BPF program, returning the value of register A.
type RetA struct{}

// Assemble implements the Instruction Assemble method.
func (a RetA) Assemble() (RawInstruction, error) {
	return RawInstruction{
		Op: opClsReturn | opRetSrcA,
	}, nil
}

// RetConstant exits the BPF program, returning a constant value.
type RetConstant struct {
	Val uint32
}

// Assemble implements the Instruction Assemble method.
func (a RetConstant) Assemble() (RawInstruction, error) {
	return RawInstruction{
		Op: opClsReturn | opRetSrcConstant,
		K:  a.Val,
	}, nil
}

// TXA copies the value of register X to register A.
type TXA struct{}

// Assemble implements the Instruction Assemble method.
func (a TXA) Assemble() (RawInstruction, error) {
	return RawInstruction{
		Op: opClsMisc | opMiscTXA,
	}, nil
}

// TAX copies the value of register A to register X.
type TAX struct{}

// Assemble implements the Instruction Assemble method.
func (a TAX) Assemble() (RawInstruction, error) {
	return RawInstruction{
		Op: opClsMisc | opMiscTAX,
	}, nil
}

func assembleLoad(dst Register, loadSize int, mode uint16, k uint32) (RawInstruction, error) {
	var (
		cls uint16
		sz  uint16
	)
	switch dst {
	case RegA:
		cls = opClsLoadA
	case RegX:
		cls = opClsLoadX
	default:
		return RawInstruction{}, fmt.Errorf("invalid target register %v", dst)
	}
	switch loadSize {
	case 1:
		sz = opLoadWidth1
	case 2:
		sz = opLoadWidth2
	case 4:
		sz = opLoadWidth4
	default:
		return RawInstruction{}, fmt.Errorf("invalid load byte length %d", sz)
	}
	return RawInstruction{
		Op: cls | sz | mode,
		K:  k,
	}, nil
}