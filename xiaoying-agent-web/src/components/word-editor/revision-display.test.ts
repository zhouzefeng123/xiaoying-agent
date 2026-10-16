import { describe, expect, it } from 'vitest';
import type { GoWordParagraphDiff } from './types';
import { buildHighlightRanges, splitTextSegments } from './revision-display';

describe('revision-display', () => {
  it('shows deleted original text once before updated content', () => {
    const diff: GoWordParagraphDiff = {
      paragraph_id: 'p1',
      type: 'updated',
      character_ranges: [{
        type: 'updated',
        before_start: 0,
        before_end: 2,
        after_start: 0,
        after_end: 2,
        before_text: '原文',
        after_text: '修改',
      }],
    };

    const ranges = buildHighlightRanges(diff);

    expect(splitTextSegments('修', 0, ranges)).toEqual([
      { text: '修', type: 'updated', deletedText: '原文' },
    ]);
    expect(splitTextSegments('改内容', 1, ranges)).toEqual([
      { text: '改', type: 'updated', deletedText: undefined },
      { text: '内容', type: 'plain', deletedText: undefined },
    ]);
  });

  it('keeps pure insertion as inserted text only', () => {
    const diff: GoWordParagraphDiff = {
      paragraph_id: 'p1',
      type: 'inserted',
      after_text: '新增内容',
      character_ranges: [{
        type: 'inserted',
        after_start: 0,
        after_end: 4,
        after_text: '新增内容',
      }],
    };

    const ranges = buildHighlightRanges(diff);

    expect(splitTextSegments('新增内容', 0, ranges)).toEqual([
      { text: '新增内容', type: 'inserted', deletedText: undefined },
    ]);
  });
});