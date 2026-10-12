// choose returns whenTrue when cond is truthy and whenFalse otherwise.
//
// It stands in for a conditional expression whose two branches are plain values (literals,
// variables, translated strings): both are evaluated before the call, so it is only used where
// evaluating the branch that is not picked has no effect and cannot fail.
export function choose<A, B>(cond: unknown, whenTrue: A, whenFalse: B): A | B {
  if (cond) {
    return whenTrue;
  }
  return whenFalse;
}
