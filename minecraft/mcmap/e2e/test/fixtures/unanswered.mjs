// Loaded ahead of the runner by a test: a fault that no check is waiting
// for, with words in it that must not come out anywhere.
setTimeout(() => { Promise.reject(new Error('RAW-FAULT at https://map.example.test/#overworld/-4321/8765/0')); }, 400);
