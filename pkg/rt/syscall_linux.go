//go:build linux

/*
 * Copyright (c) 2026 Marcin Gasperowicz <xnooga@gmail.com>
 * SPDX-License-Identifier: MIT
 */

package rt

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/nooga/let-go/pkg/vm"
)

func installSyscallNS() {
	// syscall/clone — (syscall/clone flags) → pid
	// Creates a new process with the given namespace flags.
	// flags is a bitwise OR of CLONE_* constants.
	cloneFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/clone expects 1 arg (flags)")
		}
		flags, ok := vs[0].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/clone expected Int flags")
		}
		pid, _, errno := syscall.RawSyscall(syscall.SYS_CLONE, uintptr(flags)|uintptr(syscall.SIGCHLD), 0, 0)
		if errno != 0 {
			return vm.NIL, fmt.Errorf("clone: %v", errno)
		}
		return vm.MakeInt(int(pid)), nil
	})

	// syscall/unshare — (syscall/unshare flags)
	unshareFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/unshare expects 1 arg (flags)")
		}
		flags, ok := vs[0].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/unshare expected Int flags")
		}
		if err := syscall.Unshare(int(flags)); err != nil {
			return vm.NIL, fmt.Errorf("unshare: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/mount — (syscall/mount source target fstype flags data)
	mountFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 5 {
			return vm.NIL, fmt.Errorf("syscall/mount expects 5 args (source target fstype flags data)")
		}
		source, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/mount expected String source")
		}
		target, ok := vs[1].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/mount expected String target")
		}
		fstype, ok := vs[2].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/mount expected String fstype")
		}
		flags, ok := vs[3].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/mount expected Int flags")
		}
		data, ok := vs[4].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/mount expected String data")
		}
		if err := syscall.Mount(string(source), string(target), string(fstype), uintptr(flags), string(data)); err != nil {
			return vm.NIL, fmt.Errorf("mount: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/umount — (syscall/umount target flags)
	umountFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 2 {
			return vm.NIL, fmt.Errorf("syscall/umount expects 2 args (target flags)")
		}
		target, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/umount expected String target")
		}
		flags, ok := vs[1].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/umount expected Int flags")
		}
		if err := syscall.Unmount(string(target), int(flags)); err != nil {
			return vm.NIL, fmt.Errorf("umount: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/pivot-root — (syscall/pivot-root new-root put-old)
	pivotRootFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 2 {
			return vm.NIL, fmt.Errorf("syscall/pivot-root expects 2 args (new-root put-old)")
		}
		newRoot, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/pivot-root expected String new-root")
		}
		putOld, ok := vs[1].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/pivot-root expected String put-old")
		}
		if err := syscall.PivotRoot(string(newRoot), string(putOld)); err != nil {
			return vm.NIL, fmt.Errorf("pivot-root: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/chroot — (syscall/chroot path)
	chrootFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/chroot expects 1 arg (path)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/chroot expected String path")
		}
		if err := syscall.Chroot(string(path)); err != nil {
			return vm.NIL, fmt.Errorf("chroot: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/chdir — (syscall/chdir path)
	chdirFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/chdir expects 1 arg (path)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/chdir expected String path")
		}
		if err := syscall.Chdir(string(path)); err != nil {
			return vm.NIL, fmt.Errorf("chdir: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/sethostname — (syscall/sethostname name)
	sethostnameFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/sethostname expects 1 arg")
		}
		name, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/sethostname expected String")
		}
		if err := syscall.Sethostname([]byte(string(name))); err != nil {
			return vm.NIL, fmt.Errorf("sethostname: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/exec — (syscall/exec path argv env)
	// Replaces the current process. argv is a vector of strings, env is a vector of "K=V" strings.
	execFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 3 {
			return vm.NIL, fmt.Errorf("syscall/exec expects 3 args (path argv env)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/exec expected String path")
		}
		argvSeq, ok := vs[1].(vm.Sequable)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/exec expected Sequable argv")
		}
		envSeq, ok := vs[2].(vm.Sequable)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/exec expected Sequable env")
		}
		argv := seqToStrings(argvSeq.Seq())
		env := seqToStrings(envSeq.Seq())
		if err := syscall.Exec(string(path), argv, env); err != nil {
			return vm.NIL, fmt.Errorf("exec: %v", err)
		}
		return vm.NIL, nil // unreachable on success
	})

	// syscall/getpid — (syscall/getpid)
	getpidFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		return vm.MakeInt(os.Getpid()), nil
	})

	// syscall/getuid — (syscall/getuid)
	getuidFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		return vm.MakeInt(os.Getuid()), nil
	})

	// syscall/getgid — (syscall/getgid)
	getgidFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		return vm.MakeInt(os.Getgid()), nil
	})

	// syscall/setuid — (syscall/setuid uid)
	setuidFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/setuid expects 1 arg")
		}
		uid, ok := vs[0].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/setuid expected Int")
		}
		if err := syscall.Setuid(int(uid)); err != nil {
			return vm.NIL, fmt.Errorf("setuid: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/setgid — (syscall/setgid gid)
	setgidFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/setgid expects 1 arg")
		}
		gid, ok := vs[0].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/setgid expected Int")
		}
		if err := syscall.Setgid(int(gid)); err != nil {
			return vm.NIL, fmt.Errorf("setgid: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/mkdir — (syscall/mkdir path mode)
	mkdirFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 2 {
			return vm.NIL, fmt.Errorf("syscall/mkdir expects 2 args (path mode)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/mkdir expected String path")
		}
		mode, ok := vs[1].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/mkdir expected Int mode")
		}
		if err := syscall.Mkdir(string(path), uint32(mode)); err != nil {
			return vm.NIL, fmt.Errorf("mkdir: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/waitpid — (syscall/waitpid pid options) → {:pid p :status s}
	waitpidFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 2 {
			return vm.NIL, fmt.Errorf("syscall/waitpid expects 2 args (pid options)")
		}
		pid, ok := vs[0].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/waitpid expected Int pid")
		}
		opts, ok := vs[1].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/waitpid expected Int options")
		}
		var ws syscall.WaitStatus
		rpid, err := syscall.Wait4(int(pid), &ws, int(opts), nil)
		if err != nil {
			return vm.NIL, fmt.Errorf("waitpid: %v", err)
		}
		return waitResultMapping.StructToRecord(WaitResult{
			Pid:    rpid,
			Status: ws.ExitStatus(),
		}), nil
	})

	// syscall/write-file — (syscall/write-file path content mode)
	// Useful for writing to /sys/fs/cgroup, /proc, etc.
	writeFileFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 3 {
			return vm.NIL, fmt.Errorf("syscall/write-file expects 3 args (path content mode)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/write-file expected String path")
		}
		content, ok := vs[1].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/write-file expected String content")
		}
		mode, ok := vs[2].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/write-file expected Int mode")
		}
		if err := os.WriteFile(string(path), []byte(string(content)), os.FileMode(mode)); err != nil {
			return vm.NIL, fmt.Errorf("write-file: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/read-file — (syscall/read-file path)
	readFileFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/read-file expects 1 arg (path)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/read-file expected String path")
		}
		data, err := os.ReadFile(string(path))
		if err != nil {
			return vm.NIL, fmt.Errorf("read-file: %v", err)
		}
		return vm.String(data), nil
	})

	// syscall/mkdir-p — (syscall/mkdir-p path mode)
	mkdirpFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 2 {
			return vm.NIL, fmt.Errorf("syscall/mkdir-p expects 2 args (path mode)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/mkdir-p expected String path")
		}
		mode, ok := vs[1].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/mkdir-p expected Int mode")
		}
		if err := os.MkdirAll(string(path), os.FileMode(mode)); err != nil {
			return vm.NIL, fmt.Errorf("mkdir-p: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/rmdir — (syscall/rmdir path)
	rmdirFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/rmdir expects 1 arg (path)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/rmdir expected String path")
		}
		if err := syscall.Rmdir(string(path)); err != nil {
			return vm.NIL, fmt.Errorf("rmdir: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/rm-rf — (syscall/rm-rf path)
	rmrfFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/rm-rf expects 1 arg (path)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/rm-rf expected String path")
		}
		if err := os.RemoveAll(string(path)); err != nil {
			return vm.NIL, fmt.Errorf("rm-rf: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/rm — (syscall/rm path)
	rmFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 1 {
			return vm.NIL, fmt.Errorf("syscall/rm expects 1 arg (path)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/rm expected String path")
		}
		if err := os.Remove(string(path)); err != nil {
			return vm.NIL, fmt.Errorf("rm: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/symlink — (syscall/symlink target linkpath)
	symlinkFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 2 {
			return vm.NIL, fmt.Errorf("syscall/symlink expects 2 args (target linkpath)")
		}
		target, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/symlink expected String target")
		}
		linkpath, ok := vs[1].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/symlink expected String linkpath")
		}
		if err := os.Symlink(string(target), string(linkpath)); err != nil {
			return vm.NIL, fmt.Errorf("symlink: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/chmod — (syscall/chmod path mode)
	chmodFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 2 {
			return vm.NIL, fmt.Errorf("syscall/chmod expects 2 args (path mode)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/chmod expected String path")
		}
		mode, ok := vs[1].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/chmod expected Int mode")
		}
		if err := os.Chmod(string(path), os.FileMode(mode)); err != nil {
			return vm.NIL, fmt.Errorf("chmod: %v", err)
		}
		return vm.NIL, nil
	})

	// syscall/uname — (syscall/uname) → {:sysname "Linux" :machine "aarch64" :release "6.x" :nodename "host"}
	unameFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		var buf syscall.Utsname
		if err := syscall.Uname(&buf); err != nil {
			return vm.NIL, fmt.Errorf("uname: %v", err)
		}
		return unameResultMapping.StructToRecord(UnameResult{
			Sysname:  charsToString(buf.Sysname[:]),
			Machine:  charsToString(buf.Machine[:]),
			Release:  charsToString(buf.Release[:]),
			Nodename: charsToString(buf.Nodename[:]),
		}), nil
	})

	// syscall/pipe — (syscall/pipe) → [read-handle write-handle]
	// Creates an anonymous pipe. Both ends are IOHandles (*os.File-backed).
	pipeFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 0 {
			return vm.NIL, fmt.Errorf("syscall/pipe expects 0 args")
		}
		r, w, err := os.Pipe()
		if err != nil {
			return vm.NIL, fmt.Errorf("pipe: %v", err)
		}
		return vm.ArrayVector([]vm.Value{
			vm.NewBoxed(NewIOHandle(r)),
			vm.NewBoxed(NewIOHandle(w)),
		}), nil
	})

	// syscall/kill — (syscall/kill pid sig)
	killFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 2 {
			return vm.NIL, fmt.Errorf("syscall/kill expects 2 args (pid sig)")
		}
		pid, ok := vs[0].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/kill expected Int pid")
		}
		sig, ok := vs[1].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/kill expected Int sig")
		}
		if err := syscall.Kill(int(pid), syscall.Signal(int(sig))); err != nil {
			return vm.NIL, fmt.Errorf("kill: %v", err)
		}
		return vm.NIL, nil
	})

	// stdioFile resolves a spawn stdio slot value to an *os.File.
	// nil → opens /dev/null with the given flag.
	// IOHandle → returns underlying File.
	stdioFile := func(v vm.Value, flag int) (*os.File, bool, error) {
		if v == vm.NIL || v == nil {
			f, err := os.OpenFile(os.DevNull, flag, 0)
			if err != nil {
				return nil, false, err
			}
			return f, true, nil // caller must close
		}
		h, err := getIOHandle(v)
		if err != nil {
			return nil, false, err
		}
		return h.File, false, nil
	}

	// syscall/spawn-async — (syscall/spawn-async path argv env cloneflags stdin stdout stderr)
	// Non-blocking: returns {:pid p} immediately. Caller waits via syscall/waitpid.
	// Each stdio slot is nil (→ /dev/null) or an IOHandle. The child gets a dup of
	// the underlying fd; the parent retains its handle and may close it after spawn.
	spawnAsyncFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 7 {
			return vm.NIL, fmt.Errorf("syscall/spawn-async expects 7 args (path argv env cloneflags stdin stdout stderr)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/spawn-async expected String path")
		}
		argvSeq, ok := vs[1].(vm.Sequable)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/spawn-async expected Sequable argv")
		}
		envSeq, ok := vs[2].(vm.Sequable)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/spawn-async expected Sequable env")
		}
		flags, ok := vs[3].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/spawn-async expected Int cloneflags")
		}
		argv := seqToStrings(argvSeq.Seq())
		env := seqToStrings(envSeq.Seq())

		stdinF, stdinOwn, err := stdioFile(vs[4], os.O_RDONLY)
		if err != nil {
			return vm.NIL, fmt.Errorf("spawn-async stdin: %v", err)
		}
		if stdinOwn {
			defer stdinF.Close()
		}
		stdoutF, stdoutOwn, err := stdioFile(vs[5], os.O_WRONLY)
		if err != nil {
			return vm.NIL, fmt.Errorf("spawn-async stdout: %v", err)
		}
		if stdoutOwn {
			defer stdoutF.Close()
		}
		stderrF, stderrOwn, err := stdioFile(vs[6], os.O_WRONLY)
		if err != nil {
			return vm.NIL, fmt.Errorf("spawn-async stderr: %v", err)
		}
		if stderrOwn {
			defer stderrF.Close()
		}

		proc, err := os.StartProcess(string(path), argv, &os.ProcAttr{
			Env:   env,
			Files: []*os.File{stdinF, stdoutF, stderrF},
			Sys: &syscall.SysProcAttr{
				Cloneflags: uintptr(flags),
				// Give the child its own session so it survives the parent's
				// shell/sudo tearing down the process group. Callers that want
				// tty-attached children shouldn't use spawn-async.
				Setsid: true,
			},
		})
		if err != nil {
			return vm.NIL, fmt.Errorf("spawn-async: %v", err)
		}
		// Release the *os.Process — waitpid will reap it via the kernel.
		pid := proc.Pid
		_ = proc.Release()
		return spawnResultMapping.StructToRecord(SpawnResult{Pid: pid}), nil
	})

	// syscall/spawn — (syscall/spawn path argv env cloneflags)
	// Fork+exec a child process with namespace cloneflags.
	// Returns {:pid p :exit e :out "..." :err "..."} after the child exits.
	spawnFn, _ := vm.NativeFnType.Wrap(func(vs []vm.Value) (vm.Value, error) {
		if len(vs) != 4 {
			return vm.NIL, fmt.Errorf("syscall/spawn expects 4 args (path argv env cloneflags)")
		}
		path, ok := vs[0].(vm.String)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/spawn expected String path")
		}
		argvSeq, ok := vs[1].(vm.Sequable)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/spawn expected Sequable argv")
		}
		envSeq, ok := vs[2].(vm.Sequable)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/spawn expected Sequable env")
		}
		flags, ok := vs[3].(vm.Int)
		if !ok {
			return vm.NIL, fmt.Errorf("syscall/spawn expected Int cloneflags")
		}
		argv := seqToStrings(argvSeq.Seq())
		env := seqToStrings(envSeq.Seq())

		cmd := exec.Command(string(path), argv[1:]...)
		cmd.Env = env
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags: uintptr(flags),
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return vm.NIL, fmt.Errorf("spawn: %v", err)
			}
		}
		return spawnResultMapping.StructToRecord(SpawnResult{
			Pid:  cmd.ProcessState.Pid(),
			Exit: exitCode,
			Out:  stdout.String(),
			Err:  stderr.String(),
		}), nil
	})

	ns := vm.NewNamespace("syscall")

	// namespace creation
	ns.Def("clone", cloneFn)
	ns.Def("unshare", unshareFn)

	// filesystem
	ns.Def("mount", mountFn)
	ns.Def("umount", umountFn)
	ns.Def("pivot-root", pivotRootFn)
	ns.Def("chroot", chrootFn)
	ns.Def("chdir", chdirFn)
	ns.Def("mkdir", mkdirFn)
	ns.Def("mkdir-p", mkdirpFn)
	ns.Def("rmdir", rmdirFn)
	ns.Def("rm-rf", rmrfFn)
	ns.Def("rm", rmFn)
	ns.Def("symlink", symlinkFn)
	ns.Def("chmod", chmodFn)

	// hostname
	ns.Def("sethostname", sethostnameFn)

	// process
	ns.Def("exec", execFn)
	ns.Def("spawn", spawnFn)
	ns.Def("spawn-async", spawnAsyncFn)
	ns.Def("pipe", pipeFn)
	ns.Def("kill", killFn)
	ns.Def("getpid", getpidFn)
	ns.Def("getuid", getuidFn)
	ns.Def("getgid", getgidFn)
	ns.Def("setuid", setuidFn)
	ns.Def("setgid", setgidFn)
	ns.Def("waitpid", waitpidFn)

	// system info
	ns.Def("uname", unameFn)

	// file I/O (for /proc, /sys/fs/cgroup, etc.)
	ns.Def("write-file", writeFileFn)
	ns.Def("read-file", readFileFn)

	// clone flags
	ns.Def("CLONE_NEWNS", vm.MakeInt(0x00020000))
	ns.Def("CLONE_NEWUTS", vm.MakeInt(0x04000000))
	ns.Def("CLONE_NEWIPC", vm.MakeInt(0x08000000))
	ns.Def("CLONE_NEWPID", vm.MakeInt(0x20000000))
	ns.Def("CLONE_NEWNET", vm.MakeInt(0x40000000))
	ns.Def("CLONE_NEWUSER", vm.MakeInt(0x10000000))

	// mount flags
	ns.Def("MS_BIND", vm.MakeInt(4096))
	ns.Def("MS_REC", vm.MakeInt(16384))
	ns.Def("MS_PRIVATE", vm.MakeInt(1 << 18))
	ns.Def("MS_RDONLY", vm.MakeInt(1))
	ns.Def("MS_NOSUID", vm.MakeInt(2))
	ns.Def("MS_NODEV", vm.MakeInt(4))
	ns.Def("MS_NOEXEC", vm.MakeInt(8))

	// signals
	ns.Def("SIGHUP", vm.MakeInt(1))
	ns.Def("SIGINT", vm.MakeInt(2))
	ns.Def("SIGQUIT", vm.MakeInt(3))
	ns.Def("SIGKILL", vm.MakeInt(9))
	ns.Def("SIGTERM", vm.MakeInt(15))
	ns.Def("SIGCHLD", vm.MakeInt(17))
	ns.Def("SIGWINCH", vm.MakeInt(28))

	// waitpid options
	ns.Def("WNOHANG", vm.MakeInt(1))

	RegisterNS(ns)
}

// charsToString converts a null-terminated int8 array (from Utsname) to a string.
func charsToString(ca []int8) string {
	buf := make([]byte, 0, len(ca))
	for _, c := range ca {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	return string(buf)
}

// seqToStrings converts a Seq to a []string.
func seqToStrings(s vm.Seq) []string {
	var result []string
	for s != nil {
		v := s.First()
		if str, ok := v.(vm.String); ok {
			result = append(result, string(str))
		} else {
			result = append(result, v.String())
		}
		s = s.Next()
	}
	return result
}
