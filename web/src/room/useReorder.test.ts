import { describe, expect, it } from 'vitest';
import { moveItem, sameOrder } from './useReorder';

describe('moveItem', () => {
  const list = ['a', 'b', 'c', 'd'];

  it('moves an item down and up', () => {
    expect(moveItem(list, 0, 2)).toEqual(['b', 'c', 'a', 'd']);
    expect(moveItem(list, 3, 1)).toEqual(['a', 'd', 'b', 'c']);
  });

  it('clamps rather than dropping the item off the end', () => {
    expect(moveItem(list, 0, -5)).toBe(list);
    expect(moveItem(list, 3, 99)).toBe(list);
    expect(moveItem(list, 1, 99)).toEqual(['a', 'c', 'd', 'b']);
  });

  it('returns the same array when nothing moves, so React can skip the render', () => {
    expect(moveItem(list, 2, 2)).toBe(list);
    expect(moveItem(list, 7, 0)).toBe(list);
    expect(moveItem([], 0, 1)).toEqual([]);
  });

  it('does not mutate the input', () => {
    const copy = [...list];
    moveItem(list, 0, 3);
    expect(list).toEqual(copy);
  });

  it('keeps duplicates distinct: two copies of a title are two things to watch', () => {
    expect(moveItem(['x', 'y', 'x'], 2, 0)).toEqual(['x', 'x', 'y']);
  });
});

describe('sameOrder', () => {
  it('is order-sensitive, not set equality', () => {
    expect(sameOrder(['a', 'b'], ['a', 'b'])).toBe(true);
    expect(sameOrder(['a', 'b'], ['b', 'a'])).toBe(false);
    expect(sameOrder(['a'], ['a', 'b'])).toBe(false);
    expect(sameOrder([], [])).toBe(true);
  });
});
