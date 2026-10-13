// Loaded ahead of the runner by a test: a fault that no check is waiting
// for, with words in it that must not come out anywhere.
// It waits for the runner to say it has begun, which is the line the runner
// writes once it would answer a fault. By the clock alone, a machine slow
// to load the runner has the fault come while nothing is listening for it,
// and that one is Node's to end the process on, with its words.
const write = process.stdout.write;
process.stdout.write = function (chunk, ...rest) {
  if (String(chunk).includes('"event":"start"')) {
    process.stdout.write = write;
    setTimeout(() => { Promise.reject(new Error('RAW-FAULT at https://map.example.test/#overworld/-4321/8765/0')); }, 100);
  }
  return write.call(this, chunk, ...rest);
};
