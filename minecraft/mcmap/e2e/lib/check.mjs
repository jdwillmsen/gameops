// What a check may say: it failed, with numbers; or the world held nothing
// for it to look at.

// A failure a check found. Its words are the check's own and its measures
// are numbers, so it can be logged; anything else thrown is a fault in the
// run, whose own words are kept out of the log.
export class Fail extends Error {
  constructor(detail, measures = {}) {
    super(detail);
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
