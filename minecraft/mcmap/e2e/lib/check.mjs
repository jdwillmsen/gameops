// What a check may say: it failed, with numbers; or the world held nothing
// for it to look at.

// A failure a check found. Its sentence is one of the catalogue's in
// fields.mjs, written word for word and never made up from what the page
// said, and its measures are numbers under names from the same file.
// Anything else thrown is a fault in the run.
export class Fail extends Error {
  constructor(reason, measures = {}) {
    super(reason);
    this.measures = measures;
  }
}

// A quiet world is not a broken map: a check that needs a player online or
// a mob loaded says so and is counted apart from both passing and failing.
export const skip = (reason, measures = {}) => ({ skip: reason, measures });

export const sleep = (ms) => new Promise((done) => setTimeout(done, ms));

// Until fn answers something truthy, or null when it never does.
export async function until(fn, { timeoutMs = 10_000, everyMs = 100 } = {}) {
  const end = Date.now() + timeoutMs;
  for (;;) {
    const got = await fn();
    if (got) return got;
    if (Date.now() >= end) return null;
    await sleep(everyMs);
  }
}
