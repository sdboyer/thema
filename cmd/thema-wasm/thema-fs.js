// thema-fs.js: the filesystem thema.wasm runs with in a browser, where there
// is none. Load it before Go's wasm_exec.js, which installs its own stub only
// when globalThis.fs is undefined.
//
// Every file thema reads is in a loader overlay, but CUE's loader also looks
// for optional files outside it (cue.mod/local-module.cue) and lists
// directories, and it accepts only "does not exist" as an answer. wasm_exec's
// stub answers every call with ENOSYS, and declares its open flags as -1,
// which Go reads as "directories cannot be opened at all"; either fails the
// load. Here, lookups say that nothing exists, the flags are real, and
// writes go to the console as wasm_exec's stub sends them.
(() => {
  if (globalThis.fs) {
    return;
  }
  const err = (code) => {
    const e = new Error(code);
    e.code = code;
    return e;
  };
  const enoent = (...args) => args[args.length - 1](err("ENOENT"));
  const enosys = (...args) => args[args.length - 1](err("ENOSYS"));
  const decoder = new TextDecoder("utf-8");
  let outputBuf = "";
  globalThis.fs = {
    constants: { O_WRONLY: 1, O_RDWR: 2, O_CREAT: 64, O_TRUNC: 512, O_APPEND: 1024, O_EXCL: 128, O_DIRECTORY: 65536 },
    writeSync(fd, buf) {
      outputBuf += decoder.decode(buf);
      const nl = outputBuf.lastIndexOf("\n");
      if (nl != -1) {
        console.log(outputBuf.substring(0, nl));
        outputBuf = outputBuf.substring(nl + 1);
      }
      return buf.length;
    },
    write(fd, buf, offset, length, position, callback) {
      if (offset !== 0 || length !== buf.length || position !== null) {
        callback(err("ENOSYS"));
        return;
      }
      callback(null, this.writeSync(fd, buf));
    },
    open: enoent,
    stat: enoent,
    lstat: enoent,
    readdir: enoent,
    readlink: enoent,
    fsync(fd, callback) {
      callback(null);
    },
    chmod: enosys,
    chown: enosys,
    close: enosys,
    fchmod: enosys,
    fchown: enosys,
    fstat: enosys,
    ftruncate: enosys,
    lchown: enosys,
    link: enosys,
    mkdir: enosys,
    read: enosys,
    rename: enosys,
    rmdir: enosys,
    symlink: enosys,
    truncate: enosys,
    unlink: enosys,
    utimes: enosys,
  };
})();
