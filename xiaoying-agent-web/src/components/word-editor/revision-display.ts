import type { GoWordParagraphDiff } from './types';

export type RevisionHighlightRange = {
  start: number;
  end: number;
  type: 'inserted' | 'updated';
  beforeText?: string;
};

export type RevisionTextSegment = {
  text: string;
  type: 'plain' | 'inserted' | 'updated';
  deletedText?: string;
};

export function buildHighlightRanges(diff?: GoWordParagraphDiff): RevisionHighlightRange[] {
  if (!diff) {
    return [];
  }

  const ranges = (diff.character_ranges ?? [])
    .filter((range) => range.type !== 'deleted')
    .map((range) => ({
      start: range.after_start ?? 0,
      end: range.after_end ?? 0,
      type: range.type === 'inserted' ? 'inserted' as const : 'updated' as const,
      beforeText: range.type === 'updated' ? range.before_text ?? '' : undefined,
    }))
    .filter((range) => range.end > range.start)
    .sort((left, right) => left.start - right.start);

  if (ranges.length > 0) {
    return ranges;
  }

  if (diff.type === 'inserted') {
    const textLength = Array.from(diff.after_text ?? '').length;
    if (textLength > 0) {
      return [{ start: 0, end: textLength, type: 'inserted' }];
    }
  }

  return [];
}

export function splitTextSegments(text: string, globalStart: number, ranges: RevisionHighlightRange[]): RevisionTextSegment[] {
  const chars = Array.from(text);
  if (chars.length === 0) {
    return [];
  }

  const boundaries = new Set<number>([0, chars.length]);
  for (const range of ranges) {
    const start = Math.max(0, Math.min(chars.length, range.start - globalStart));
    const end = Math.max(0, Math.min(chars.length, range.end - globalStart));
    if (end <= 0 || start >= chars.length || end <= start) {
      continue;
    }
    boundaries.add(start);
    boundaries.add(end);
  }

  const ordered = Array.from(boundaries).sort((left, right) => left - right);
  const segments: RevisionTextSegment[] = [];

  for (let index = 0; index < ordered.length - 1; index += 1) {
    const start = ordered[index];
    const end = ordered[index + 1];
    if (end <= start) {
      continue;
    }

    const absoluteStart = globalStart + start;
    const absoluteEnd = globalStart + end;
    const highlight = ranges.find((range) => absoluteStart >= range.start && absoluteEnd <= range.end);
    segments.push({
      text: chars.slice(start, end).join(''),
      type: highlight?.type ?? 'plain',
      deletedText: highlight?.type === 'updated' && absoluteStart === highlight.start ? highlight.beforeText : undefined,
    });
  }

  return segments;
}