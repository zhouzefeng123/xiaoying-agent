import React, { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, FileImage, Loader2 } from 'lucide-react';
import { cn } from '../../lib/utils';
import {
  buildGoWordImageURL,
  closeGoWordSession,
  executeGoWordActionPlan,
  getGoWordDocument,
  getGoWordLayout,
  getAbsoluteFilePath,
  openGoWordDocument,
  planGoWordAction,
  saveGoWordSession,
  uploadWordDocument,
} from './goword-client';
import { publishWordEditorApplyError, publishWordEditorSession, setWordUnsavedChanges } from './word-editor-bus';
import type {
  GoWordActionDiagnostics,
  GoWordActionPlanResult,
  GoWordActionValidation,
  GoWordBlock,
  GoWordCharacterRangeDiff,
  GoWordDocument,
  GoWordDocumentLayout,
  GoWordDocumentSection,
  GoWordHeaderFooterInput,
  GoWordHeaderFooterPart,
  GoWordParagraph,
  GoWordParagraphDiff,
  GoWordParagraphStyle,
  GoWordRun,
  GoWordSectionLayoutPatch,
  GoWordTable,
  GoWordTableRow,
  GoWordTextStyle,
  WordEditorActionPayload,
  WordEditorDocumentState,
} from './types';
import {
  applyPageNumberText,
  buildPageNumberContexts,
  pageContentHeightFromLayout,
  pageContentWidthFromLayout,
  pickHeaderFooterPart,
  resolvePageLayout,
  sectionBreakForcesPageBreak,
  sectionKey,
  sectionStartNeedsBlankPage,
  twipsToPx,
  type PageNumberContext,
  type ResolvedPageLayout,
} from './word-pagination';
import { buildHighlightRanges, splitTextSegments } from './revision-display';

interface WordPreviewProps {
  document: WordEditorDocumentState | null;
  lastAction: WordEditorActionPayload | null;
  actionVersion: number;
  onToolbarStateChange?: (state: WordPreviewToolbarState) => void;
}

export interface WordPreviewHandle {
  saveDocument: (saveAs: boolean) => Promise<boolean>;
}

export interface WordPreviewToolbarState {
  canSave: boolean;
  isBusy: boolean;
  isSaving: boolean;
  sessionId: string | null;
}

type RevisionDecision = 'pending' | 'kept';

type WordRevision = {
  id: string;
  action: WordEditorActionPayload;
  replayAction: WordEditorActionPayload;
  diffs: GoWordParagraphDiff[];
  activeParagraphId: string | null;
  statusText: string;
  decision: RevisionDecision;
};

type ParagraphRevisionSummary = {
  id: string;
  revisionId: string;
  decision: RevisionDecision;
  diff: GoWordCharacterRangeDiff;
};

type DeletedRevisionInlineEntry = ParagraphRevisionSummary & {
  anchorParagraphId: string | null;
};

type PageRenderContext = {
  pageNumber: number;
  displayedPageNumber: number;
  sectionPageOrdinal: number;
  pageNumberFormat?: string;
  totalPages: number;
  isHeaderFooter?: boolean;
};

type PreviewFlowItem = {
  kind: 'block' | 'table';
  block: GoWordBlock;
  key: string;
  sourceBlockIndex: number;
  table?: GoWordTable;
  keepWithNext?: boolean;
  forcePageBreakBefore?: boolean;
  forcePageBreakAfter?: boolean;
};

type TableFragmentItem = {
  kind: 'table-fragment';
  key: string;
  sourceBlockIndex: number;
  table: GoWordTable;
  rowIndexes: number[];
  headerRowCount: number;
  rowClips?: Record<number, { start: number; height: number }>;
};

type BlockPageItem = {
  kind: 'block';
  key: string;
  sourceBlockIndex: number;
  block: GoWordBlock;
  keepWithNext?: boolean;
};

type ParagraphFragmentPageItem = {
  kind: 'paragraph-fragment';
  key: string;
  sourceBlockIndex: number;
  paragraph: GoWordParagraph;
  startLine: number;
  lineCount: number;
  offsetTop: number;
  fragmentHeight: number;
  keepWithNext?: boolean;
};

type PageRenderItem = BlockPageItem | TableFragmentItem | ParagraphFragmentPageItem;

type PagePreviewLayout = {
	items: PageRenderItem[];
	section: GoWordDocumentSection | null;
  insertedBlankPage?: boolean;
};

type PendingPageItem = {
  item: PageRenderItem;
  height: number;
  keepWithNext?: boolean;
};

type WordActionErrorState = {
  message: string;
  diagnostics?: GoWordActionDiagnostics;
  validation?: GoWordActionValidation;
  action?: WordEditorActionPayload;
  plan?: GoWordActionPlanResult;
};

class WordActionExecutionError extends Error {
  diagnostics?: GoWordActionDiagnostics;
  validation?: GoWordActionValidation;
  plan?: GoWordActionPlanResult;

  constructor(message: string, options?: { diagnostics?: GoWordActionDiagnostics; validation?: GoWordActionValidation; plan?: GoWordActionPlanResult }) {
    super(message);
    this.name = 'WordActionExecutionError';
    this.diagnostics = options?.diagnostics;
    this.validation = options?.validation;
    this.plan = options?.plan;
  }
}

type ParagraphPreviewSegment = {
  paragraph: GoWordParagraph;
  keepWithNext?: boolean;
  forcePageBreakBefore?: boolean;
  forcePageBreakAfter?: boolean;
};

function buildMockDocument(name: string): GoWordDocument {
  const paragraphs: GoWordParagraph[] = [
    {
      id: 'mock-para-1',
      style: { style_id: 'Title', alignment: 'center' },
      runs: [{ id: 'mock-run-1', text: 'XiaoYing Word 智能编辑示例', style: { bold: true, font_size_hp: 36 } }],
    },
    {
      id: 'mock-para-2',
      style: { spacing_after: 240 },
      runs: [{ id: 'mock-run-2', text: '当前示例模式仅用于预览结构。打开真实 docx 后，右侧面板会直接连接 go-word 会话并执行精确编辑。', style: {} }],
    },
    {
      id: 'mock-para-3',
      style: {},
      runs: [{ id: 'mock-run-3', text: 'Agent 发出的 replace / insert / delete / scroll 指令会命中真实段落并返回字符级差异。', style: {} }],
    },
  ];

  return {
    id: 'mock-word-document',
    name,
    revision: 1,
    paragraphs,
    blocks: paragraphs.map((paragraph) => ({ type: 'paragraph' as const, paragraph })),
  };
}

function paragraphPlainText(paragraph: GoWordParagraph): string {
  return paragraph.runs.map((run) => run.text ?? '').join('');
}

function canUseInlineTextEditing(paragraph: GoWordParagraph): boolean {
  if (paragraph.runs.length === 0) {
    return false;
  }

  return paragraph.runs.every((run) => !run.image && !run.drawing_xml && !run.break_type);
}

function buildLiveParagraphDiff(paragraphId: string, beforeText: string, afterText: string): GoWordParagraphDiff | null {
  if (beforeText === afterText) {
    return null;
  }

  const beforeChars = Array.from(beforeText);
  const afterChars = Array.from(afterText);
  let prefixLength = 0;
  while (
    prefixLength < beforeChars.length
    && prefixLength < afterChars.length
    && beforeChars[prefixLength] === afterChars[prefixLength]
  ) {
    prefixLength += 1;
  }

  let beforeSuffix = beforeChars.length;
  let afterSuffix = afterChars.length;
  while (
    beforeSuffix > prefixLength
    && afterSuffix > prefixLength
    && beforeChars[beforeSuffix - 1] === afterChars[afterSuffix - 1]
  ) {
    beforeSuffix -= 1;
    afterSuffix -= 1;
  }

  const beforeChanged = beforeChars.slice(prefixLength, beforeSuffix).join('');
  const afterChanged = afterChars.slice(prefixLength, afterSuffix).join('');
  const diffType: GoWordCharacterRangeDiff['type'] = beforeChanged.length === 0
    ? 'inserted'
    : afterChanged.length === 0
      ? 'deleted'
      : 'updated';

  return {
    paragraph_id: paragraphId,
    type: diffType,
    before_text: beforeText,
    after_text: afterText,
    character_ranges: [{
      type: diffType,
      before_start: prefixLength,
      before_end: beforeSuffix,
      after_start: prefixLength,
      after_end: afterSuffix,
      before_text: beforeChanged,
      after_text: afterChanged,
    }],
  };
}

function normalizeMatchText(text: string): string {
  return text.replace(/\s+/g, '').trim();
}

function normalizeColor(color?: string): string | undefined {
  if (!color) {
    return undefined;
  }
  if (color.startsWith('#')) {
    return color;
  }
  if (/^[0-9a-fA-F]{6}$/.test(color)) {
    return `#${color}`;
  }
  return undefined;
}

function resolveDocumentSections(document: GoWordDocument | null): GoWordDocumentSection[] {
  const sections = document?.meta?.sections ?? [];
  if (sections.length > 0) {
    return sections;
  }

  const totalBlocks = document?.blocks?.length ?? document?.paragraphs.length ?? 0;
  if (totalBlocks === 0) {
    return [];
  }

  return [{
    start_block_index: 0,
    end_block_index: totalBlocks - 1,
    layout: document?.meta?.section_layout,
    header_footers: document?.meta?.header_footers,
  }];
}

function findSectionForBlockIndex(sections: GoWordDocumentSection[], blockIndex: number): GoWordDocumentSection | null {
  for (const section of sections) {
    const startBlockIndex = section.start_block_index ?? 0;
    const endBlockIndex = section.end_block_index ?? Number.MAX_SAFE_INTEGER;
    if (blockIndex >= startBlockIndex && blockIndex <= endBlockIndex) {
      return section;
    }
  }

  return sections[sections.length - 1] ?? null;
}

function headerFooterHasPageNumber(part: GoWordHeaderFooterPart | null): boolean {
  if (!part) {
    return false;
  }
  const paragraphs = part.paragraphs ?? [];
  return paragraphs.some((paragraph) => /第\s*\d+\s*页|\{\{PAGE\}\}/.test(paragraphPlainText(paragraph)));
}

function buildParagraphStyle(paragraph: GoWordParagraph): React.CSSProperties {
  const alignment = paragraph.style.alignment === 'both' ? 'justify' : paragraph.style.alignment;
  return {
    textAlign: alignment as React.CSSProperties['textAlign'],
    marginTop: twipsToPx(paragraph.style.spacing_before),
    marginBottom: twipsToPx(paragraph.style.spacing_after) ?? 10,
    paddingLeft: twipsToPx(paragraph.style.left_indent),
    textIndent: twipsToPx(paragraph.style.first_line_indent),
    lineHeight: paragraph.style.line_spacing ? Math.max(paragraph.style.line_spacing / 240, 1.2) : 1.7,
  };
}

function splitParagraphForPreview(paragraph: GoWordParagraph, currentSection: GoWordDocumentSection | null): ParagraphPreviewSegment[] {
  const segments: ParagraphPreviewSegment[] = [];
  let currentRuns: GoWordRun[] = [];
  let forcePageBreakBefore = Boolean(paragraph.style.page_break_before);
  let sawPageBreak = false;

  for (const run of paragraph.runs) {
    if (run.break_type === 'page' || run.break_type === 'renderedPage') {
      sawPageBreak = true;
      if (currentRuns.length > 0) {
        segments.push({
          paragraph: {
            ...paragraph,
            runs: currentRuns,
          },
          keepWithNext: false,
          forcePageBreakBefore,
        });
        currentRuns = [];
      }
      forcePageBreakBefore = true;
      continue;
    }
    currentRuns.push(run);
  }

  if (!sawPageBreak) {
      return [{
        paragraph,
        keepWithNext: Boolean(paragraph.style.keep_next),
        forcePageBreakBefore,
        forcePageBreakAfter: sectionBreakForcesPageBreak(paragraph, currentSection),
      }];
  }

  if (currentRuns.length > 0) {
    segments.push({
      paragraph: {
        ...paragraph,
        runs: currentRuns,
      },
      keepWithNext: Boolean(paragraph.style.keep_next),
      forcePageBreakBefore,
      forcePageBreakAfter: sectionBreakForcesPageBreak(paragraph, currentSection),
    });
  } else if (segments.length > 0 && forcePageBreakBefore) {
    segments[segments.length - 1].forcePageBreakAfter = true;
  }

  if (segments.length === 0) {
    return [{
      paragraph: {
        ...paragraph,
        runs: [{ id: `${paragraph.id}-empty`, text: '', style: {} }],
      },
      keepWithNext: Boolean(paragraph.style.keep_next),
      forcePageBreakBefore: true,
      forcePageBreakAfter: sectionBreakForcesPageBreak(paragraph, currentSection),
    }];
  }

  return segments;
}

function buildPreviewFlowItems(blocks: GoWordBlock[], sections: GoWordDocumentSection[]): PreviewFlowItem[] {
  const previewItems: PreviewFlowItem[] = [];

  blocks.forEach((block, index) => {
    if (block.type === 'paragraph' && block.paragraph) {
      const segments = splitParagraphForPreview(block.paragraph, findSectionForBlockIndex(sections, index));
      segments.forEach((segment, segmentIndex) => {
        previewItems.push({
          kind: 'block',
          block: {
            type: 'paragraph',
            paragraph: segment.paragraph,
          },
          key: `${block.paragraph?.id ?? 'paragraph'}-${index}-${segmentIndex}`,
          sourceBlockIndex: index,
          keepWithNext: segment.keepWithNext,
          forcePageBreakBefore: segment.forcePageBreakBefore,
          forcePageBreakAfter: segment.forcePageBreakAfter,
        });
      });
      return;
    }

    previewItems.push({
      kind: block.type === 'table' && block.table ? 'table' : 'block',
      block,
      table: block.table,
      key: `${block.type}-${index}`,
      sourceBlockIndex: index,
    });
  });

  return previewItems;
}

function countRepeatedHeaderRows(rows: GoWordTableRow[]): number {
  let count = 0;
  for (const row of rows) {
    if (!row.repeat_header) {
      break;
    }
    count += 1;
  }
  return count;
}

// 解析 <w:tblGrid> 中每个 <w:gridCol w:w="N"/> 的 twips 列宽，用于还原 Word 表格的列宽布局。
const GRID_COL_WIDTH_RE = /<w:gridCol[^>]*\sw:w="(\d+)"/g;
function parseTableGridColTwips(gridXml?: string): number[] {
  if (!gridXml) return [];
  const widths: number[] = [];
  GRID_COL_WIDTH_RE.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = GRID_COL_WIDTH_RE.exec(gridXml)) !== null) {
    const value = parseInt(match[1], 10);
    if (Number.isFinite(value) && value > 0) widths.push(value);
  }
  return widths;
}

// 解析单元格 <w:gridSpan w:val="N"/>，用于跨列合并。无则返回 1。
function parseCellGridSpan(propertiesXml?: string): number {
  if (!propertiesXml) return 1;
  const match = /<w:gridSpan[^>]*\sw:val="(\d+)"/.exec(propertiesXml);
  if (!match) return 1;
  const value = parseInt(match[1], 10);
  return Number.isFinite(value) && value > 1 ? value : 1;
}

function buildFallbackPageLayout(items: PreviewFlowItem[]): PageRenderItem[] {
  return items.flatMap((item) => previewItemToPageItems(item));
}

function previewItemToPageItems(item: PreviewFlowItem): PageRenderItem[] {
  if (item.kind === 'table' && item.table) {
    return [{
      kind: 'table-fragment',
      key: `${item.key}-fallback`,
      sourceBlockIndex: item.sourceBlockIndex,
      table: item.table,
      rowIndexes: item.table.rows.map((_, rowIndex) => rowIndex),
      headerRowCount: countRepeatedHeaderRows(item.table.rows),
    } satisfies TableFragmentItem];
  }

  return [{
    kind: 'block',
    key: item.key,
    sourceBlockIndex: item.sourceBlockIndex,
    block: item.block,
    keepWithNext: item.keepWithNext,
  } satisfies BlockPageItem];
}

function buildPageLayoutsFromBackendLayout(
  layout: GoWordDocumentLayout | null,
  previewItems: PreviewFlowItem[],
  documentSections: GoWordDocumentSection[],
): PagePreviewLayout[] {
  if (!layout || layout.pages.length === 0) {
    return [];
  }

  const itemsByBlockIndex = new Map<number, PageRenderItem[]>();
  previewItems.forEach((item) => {
    const existing = itemsByBlockIndex.get(item.sourceBlockIndex) ?? [];
    existing.push(...previewItemToPageItems(item));
    itemsByBlockIndex.set(item.sourceBlockIndex, existing);
  });

  const laidOutPages = layout.pages.map((page) => {
    const usedIndexes = new Set<number>();
    const items: PageRenderItem[] = [];
    (page.blocks ?? []).forEach((block) => {
      if (usedIndexes.has(block.source_block_index)) {
        return;
      }
      const mappedItems = itemsByBlockIndex.get(block.source_block_index) ?? [];
      items.push(...mappedItems);
      usedIndexes.add(block.source_block_index);
    });
    return {
      items,
      section: findSectionForBlockIndex(documentSections, page.blocks?.[0]?.source_block_index ?? 0),
    } satisfies PagePreviewLayout;
  });

  const expandedPages: PagePreviewLayout[] = [];
  laidOutPages.forEach((page) => {
    const previousPage = expandedPages[expandedPages.length - 1] ?? null;
    const isSectionStart = !previousPage || !sameSection(previousPage.section, page.section);
    if (isSectionStart && sectionStartNeedsBlankPage(page.section?.layout, expandedPages.length + 1)) {
      expandedPages.push({
        items: [],
        section: previousPage?.section ?? page.section,
        insertedBlankPage: true,
      });
    }
    expandedPages.push(page);
  });

  return expandedPages;
}

function sameSection(left: GoWordDocumentSection | null | undefined, right: GoWordDocumentSection | null | undefined): boolean {
  return sectionKey(left) === sectionKey(right);
}

function paragraphAllowsLineSplit(paragraph: GoWordParagraph, lineCount: number): boolean {
  if (lineCount <= 1) {
    return false;
  }
  if (paragraph.style.keep_lines) {
    return false;
  }
  return true;
}

function resolveWidowControlLines(paragraph: GoWordParagraph): number {
  if (paragraph.style.widow_control_set && paragraph.style.widow_control === false) {
    return 1;
  }
  return 2;
}

type ParagraphLineBox = {
  top: number;
  bottom: number;
};

function mergeLineRects(existingLines: ParagraphLineBox[], top: number, bottom: number) {
  const tolerance = 2;
  const targetMidpoint = (top + bottom) / 2;
  const matchedLine = existingLines.find((line) => {
    const lineMidpoint = (line.top + line.bottom) / 2;
    return Math.abs(lineMidpoint - targetMidpoint) <= tolerance
      || (top <= line.bottom + tolerance && bottom >= line.top - tolerance);
  });
  if (matchedLine) {
    matchedLine.top = Math.min(matchedLine.top, top);
    matchedLine.bottom = Math.max(matchedLine.bottom, bottom);
    return;
  }
  existingLines.push({ top, bottom });
}

function collectParagraphLineBoxes(rootNode: HTMLElement, paragraphTextNode: HTMLElement): ParagraphLineBox[] {
  const rootRect = rootNode.getBoundingClientRect();
  const lines: ParagraphLineBox[] = [];

  const visitNode = (node: Node) => {
    if (node.nodeType === Node.TEXT_NODE) {
      const content = node.textContent ?? '';
      if (!content) {
        return;
      }
      const range = document.createRange();
      range.selectNodeContents(node);
      const rects = Array.from(range.getClientRects());
      rects.forEach((rect) => {
        if (rect.height <= 0 || rect.width <= 0) {
          return;
        }
        mergeLineRects(lines, rect.top - rootRect.top, rect.bottom - rootRect.top);
      });
      range.detach?.();
      return;
    }

    if (!(node instanceof HTMLElement)) {
      return;
    }

    if (node.childNodes.length === 0) {
      const rects = Array.from(node.getClientRects());
      rects.forEach((rect) => {
        if (rect.height <= 0 || rect.width <= 0) {
          return;
        }
        mergeLineRects(lines, rect.top - rootRect.top, rect.bottom - rootRect.top);
      });
      return;
    }

    node.childNodes.forEach((childNode) => visitNode(childNode));
  };

  visitNode(paragraphTextNode);
  lines.sort((left, right) => left.top - right.top);
  return lines;
}

function buildParagraphFragments(
  paragraph: GoWordParagraph,
  baseKey: string,
  sourceBlockIndex: number,
  lineBoxes: ParagraphLineBox[],
  totalHeight: number,
  pageHeightLimit: number,
  pageRemainingHeight: number,
): ParagraphFragmentPageItem[] {
  const effectiveLineBoxes = lineBoxes.length > 0
    ? lineBoxes
    : [{ top: 0, bottom: totalHeight }];
  const totalLines = Math.max(1, effectiveLineBoxes.length);
  if (!paragraphAllowsLineSplit(paragraph, totalLines)) {
    return [];
  }

  const widowLines = Math.min(resolveWidowControlLines(paragraph), totalLines);
  const fragments: ParagraphFragmentPageItem[] = [];
  let startLine = 0;
  let remainingLines = totalLines;
  let availableHeight = Math.max(pageRemainingHeight, effectiveLineBoxes[0].bottom - effectiveLineBoxes[0].top);

  while (remainingLines > 0) {
    const startTop = effectiveLineBoxes[startLine].top;
    let linesThisPage = 0;
    while (startLine + linesThisPage < totalLines) {
      const candidateLine = effectiveLineBoxes[startLine + linesThisPage];
      if (candidateLine.bottom - startTop > availableHeight + 1 && linesThisPage > 0) {
        break;
      }
      linesThisPage += 1;
      if (candidateLine.bottom - startTop > availableHeight + 1) {
        break;
      }
    }

    linesThisPage = Math.max(1, Math.min(linesThisPage, remainingLines));
    if (linesThisPage < remainingLines && remainingLines > widowLines) {
      if (linesThisPage < widowLines) {
        linesThisPage = Math.min(remainingLines, widowLines);
      }
      const trailingLines = remainingLines - linesThisPage;
      if (trailingLines > 0 && trailingLines < widowLines) {
        linesThisPage = Math.max(widowLines, remainingLines - widowLines);
      }
    }

    const endLine = startLine + linesThisPage - 1;
    const fragmentTop = effectiveLineBoxes[startLine].top;
    const fragmentBottom = effectiveLineBoxes[endLine].bottom;
    const fragmentHeight = Math.max(1, fragmentBottom - fragmentTop);

    fragments.push({
      kind: 'paragraph-fragment',
      key: `${baseKey}-lines-${startLine}-${linesThisPage}`,
      sourceBlockIndex,
      paragraph,
      startLine,
      lineCount: linesThisPage,
      offsetTop: fragmentTop,
      fragmentHeight,
      keepWithNext: false,
    });

    startLine += linesThisPage;
    remainingLines -= linesThisPage;
    availableHeight = pageHeightLimit;
  }

  return fragments;
}

function findRuneRange(content: string, target: string): { start: number; end: number } | null {
  const matches = collectRuneRanges(content, target);
  return matches[0] ?? null;
}

function collectRuneRanges(content: string, target: string): Array<{ start: number; end: number }> {
  const source = Array.from(content);
  const needle = Array.from(target);
  if (needle.length === 0 || source.length < needle.length) {
    return [];
  }

  const ranges: Array<{ start: number; end: number }> = [];
  outer: for (let index = 0; index <= source.length - needle.length; index += 1) {
    for (let offset = 0; offset < needle.length; offset += 1) {
      if (source[index + offset] !== needle[offset]) {
        continue outer;
      }
    }
    ranges.push({ start: index, end: index + needle.length });
  }

  return ranges;
}

function findNormalizedRuneRange(content: string, target: string): { start: number; end: number } | null {
  const matches = collectNormalizedRuneRanges(content, target);
  return matches[0] ?? null;
}

function collectNormalizedRuneRanges(content: string, target: string): Array<{ start: number; end: number }> {
  const source = Array.from(content);
  const compactSource: string[] = [];
  const compactIndexes: number[] = [];

  for (let index = 0; index < source.length; index += 1) {
    const character = source[index];
    if (/\s/.test(character)) {
      continue;
    }
    compactSource.push(character);
    compactIndexes.push(index);
  }

  const compactTarget = Array.from(normalizeMatchText(target));
  if (compactTarget.length === 0 || compactSource.length < compactTarget.length) {
    return [];
  }

  const ranges: Array<{ start: number; end: number }> = [];
  outer: for (let index = 0; index <= compactSource.length - compactTarget.length; index += 1) {
    for (let offset = 0; offset < compactTarget.length; offset += 1) {
      if (compactSource[index + offset] !== compactTarget[offset]) {
        continue outer;
      }
    }

    const start = compactIndexes[index];
    const end = compactIndexes[index + compactTarget.length - 1] + 1;
    ranges.push({ start, end });
  }

  return ranges;
}

function extractRuneSlice(content: string, start: number, end: number): string {
  return Array.from(content).slice(start, end).join('');
}

function locateParagraph(document: GoWordDocument, targetText: string): GoWordParagraph | null {
  const normalizedTarget = targetText.trim();
  if (!normalizedTarget) {
    return document.paragraphs[document.paragraphs.length - 1] ?? null;
  }

  for (const paragraph of document.paragraphs) {
    if (paragraphPlainText(paragraph).includes(normalizedTarget)) {
      return paragraph;
    }
  }

  const compactTarget = normalizeMatchText(normalizedTarget);
  if (!compactTarget) {
    return null;
  }

  for (const paragraph of document.paragraphs) {
    if (normalizeMatchText(paragraphPlainText(paragraph)).includes(compactTarget)) {
      return paragraph;
    }
  }

  return null;
}

function locateParagraphById(document: GoWordDocument, paragraphId?: string): GoWordParagraph | null {
  if (!paragraphId) {
    return null;
  }
  return document.paragraphs.find((paragraph) => paragraph.id === paragraphId) ?? null;
}

function mergeParagraphStylePatch(base: GoWordParagraphStyle | undefined, patch: GoWordParagraphStyle | undefined): GoWordParagraphStyle | undefined {
  if (!base && !patch) {
    return undefined;
  }
  return {
    ...(base ?? {}),
    ...(patch ?? {}),
    keep_next_set: patch?.keep_next_set ?? base?.keep_next_set,
    keep_next: patch?.keep_next ?? base?.keep_next,
    keep_lines_set: patch?.keep_lines_set ?? base?.keep_lines_set,
    keep_lines: patch?.keep_lines ?? base?.keep_lines,
    page_break_before_set: patch?.page_break_before_set ?? base?.page_break_before_set,
    page_break_before: patch?.page_break_before ?? base?.page_break_before,
    widow_control_set: patch?.widow_control_set ?? base?.widow_control_set,
    widow_control: patch?.widow_control ?? base?.widow_control,
    numbering: patch?.numbering ?? base?.numbering,
  };
}

function describeParagraphStylePatch(stylePatch?: GoWordParagraphStyle): string {
  if (!stylePatch) {
    return '段落样式';
  }
  const labels: string[] = [];
  if (stylePatch.style_id) {
    labels.push(`样式 ${stylePatch.style_id}`);
  }
  if (stylePatch.alignment) {
    labels.push(`对齐 ${stylePatch.alignment}`);
  }
  if (typeof stylePatch.spacing_before === 'number') {
    labels.push(`段前 ${stylePatch.spacing_before}`);
  }
  if (typeof stylePatch.spacing_after === 'number') {
    labels.push(`段后 ${stylePatch.spacing_after}`);
  }
  if (typeof stylePatch.keep_next === 'boolean') {
    labels.push(stylePatch.keep_next ? '与下段同页' : '取消与下段同页');
  }
  if (typeof stylePatch.keep_lines === 'boolean') {
    labels.push(stylePatch.keep_lines ? '段内行不拆分' : '取消段内行不拆分');
  }
  if (typeof stylePatch.page_break_before === 'boolean') {
    labels.push(stylePatch.page_break_before ? '段前分页' : '取消段前分页');
  }
  if (typeof stylePatch.widow_control === 'boolean') {
    labels.push(stylePatch.widow_control ? '孤行控制' : '取消孤行控制');
  }
  if (stylePatch.numbering?.num_id) {
    labels.push(`编号 ${stylePatch.numbering.num_id}`);
  }
  return labels.length > 0 ? labels.join('、') : '段落样式';
}

function hasSectionLayoutPatch(sectionLayoutPatch?: GoWordSectionLayoutPatch): boolean {
  if (!sectionLayoutPatch) {
    return false;
  }
  return Boolean(
    sectionLayoutPatch.page_width
    || sectionLayoutPatch.page_height
    || sectionLayoutPatch.orientation
    || sectionLayoutPatch.margin_top
    || sectionLayoutPatch.margin_right
    || sectionLayoutPatch.margin_bottom
    || sectionLayoutPatch.margin_left
    || sectionLayoutPatch.margin_header
    || sectionLayoutPatch.margin_footer
    || sectionLayoutPatch.margin_gutter,
  );
}

function describeHeaderFooterTarget(headerFooter?: GoWordHeaderFooterInput): string {
  if (!headerFooter) {
    return '页眉页脚';
  }
  const kindLabel = headerFooter.kind === 'footer' ? '页脚' : '页眉';
  const variantLabel = headerFooter.variant === 'first'
    ? '首页'
    : headerFooter.variant === 'even'
      ? '偶数页'
      : '默认';
  return `${variantLabel}${kindLabel}`;
}

function findHeaderFooterPart(document: GoWordDocument, headerFooter?: GoWordHeaderFooterInput): GoWordHeaderFooterPart | null {
  if (!headerFooter) {
    return null;
  }
  return document.meta?.header_footers?.find((part) => (
    part.kind === headerFooter.kind && (part.variant ?? 'default') === (headerFooter.variant ?? 'default')
  )) ?? null;
}

function describeSectionLayoutPatch(sectionLayoutPatch?: GoWordSectionLayoutPatch): string {
  if (!sectionLayoutPatch) {
    return '页面版式';
  }
  const labels: string[] = [];
  if (sectionLayoutPatch.orientation) {
    labels.push(sectionLayoutPatch.orientation === 'landscape' ? '横向页面' : '纵向页面');
  }
  if (sectionLayoutPatch.page_width && sectionLayoutPatch.page_height) {
    labels.push(`页面尺寸 ${sectionLayoutPatch.page_width} x ${sectionLayoutPatch.page_height}`);
  }
  if (typeof sectionLayoutPatch.margin_left === 'number' || typeof sectionLayoutPatch.margin_right === 'number') {
    labels.push('左右页边距');
  }
  if (typeof sectionLayoutPatch.margin_top === 'number' || typeof sectionLayoutPatch.margin_bottom === 'number') {
    labels.push('上下页边距');
  }
  if (typeof sectionLayoutPatch.margin_header === 'number' || typeof sectionLayoutPatch.margin_footer === 'number') {
    labels.push('页眉页脚边距');
  }
  return labels.length > 0 ? labels.join('、') : '页面版式';
}

function describeStylePatch(stylePatch?: GoWordTextStyle): string {
  if (!stylePatch) {
    return '样式';
  }
  const labels: string[] = [];
  if (stylePatch.font_family) {
    labels.push(`字体 ${stylePatch.font_family}`);
  }
  if (stylePatch.font_size_hp) {
    labels.push(`字号 ${stylePatch.font_size_hp / 2} 磅`);
  }
  if (stylePatch.bold_set || stylePatch.bold) {
    labels.push(stylePatch.bold ? '加粗' : '取消加粗');
  }
  if (stylePatch.italic_set || stylePatch.italic) {
    labels.push(stylePatch.italic ? '斜体' : '取消斜体');
  }
  if (stylePatch.underline_set || stylePatch.underline) {
    labels.push(stylePatch.underline ? '下划线' : '取消下划线');
  }
  if (stylePatch.color) {
    labels.push(`颜色 ${stylePatch.color}`);
  }
  return labels.length > 0 ? labels.join('、') : '样式';
}

function changeSummary(diff?: GoWordParagraphDiff): GoWordCharacterRangeDiff[] {
  if (!diff) {
    return [];
  }

  const ranges = (diff.character_ranges ?? []).filter((range) => range.type !== 'unchanged').slice(0, 4);
  if (ranges.length > 0) {
    return ranges;
  }

  if (diff.type === 'inserted' || diff.type === 'deleted' || diff.type === 'updated') {
    return [{
      type: diff.type,
      before_text: diff.before_text,
      after_text: diff.after_text,
    }];
  }

  return [];
}

function resolveFocusParagraphId(
  diffs: GoWordParagraphDiff[] | undefined,
  fallbackParagraphId: string | null,
): string | null {
  const preferredDiff = (diffs ?? []).find((diff) => diff.type === 'inserted' || diff.type === 'updated');
  if (preferredDiff?.paragraph_id) {
    return preferredDiff.paragraph_id;
  }

  const firstDiffParagraphId = diffs?.[0]?.paragraph_id ?? null;
  return firstDiffParagraphId ?? fallbackParagraphId;
}

function formatSummaryItem(item: GoWordCharacterRangeDiff): string {
  if (item.type === 'deleted') {
    const deletedText = (item.before_text ?? '').trim();
    if (!deletedText) {
      return '删除';
    }
    const preview = deletedText.length > 24 ? `${deletedText.slice(0, 24)}...` : deletedText;
    return `删除：${preview}`;
  }
  if (item.type === 'inserted') {
    return '新增';
  }
  if (item.type === 'updated') {
    return '替换';
  }
  return '变更';
}

function createRevisionId(actionVersion: number): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `rev-${actionVersion}-${crypto.randomUUID()}`;
  }
  return `rev-${actionVersion}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function toWordActionErrorState(error: unknown, fallbackMessage: string, action?: WordEditorActionPayload): WordActionErrorState {
  if (error instanceof WordActionExecutionError) {
    return {
      message: error.message || fallbackMessage,
      diagnostics: error.diagnostics,
      validation: error.validation,
      action,
      plan: error.plan,
    };
  }

  if (error instanceof Error) {
    return { message: error.message || fallbackMessage, action };
  }

  return { message: fallbackMessage, action };
}

export const WordPreview = forwardRef<WordPreviewHandle, WordPreviewProps>(({ document, lastAction, actionVersion, onToolbarStateChange }, ref) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const sessionIdRef = useRef<string | null>(null);
  const appliedActionVersionRef = useRef(0);
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [snapshot, setSnapshot] = useState<GoWordDocument | null>(null);
  const [layoutSnapshot, setLayoutSnapshot] = useState<GoWordDocumentLayout | null>(null);
  const [revisions, setRevisions] = useState<WordRevision[]>([]);
  const [activeParagraphId, setActiveParagraphId] = useState<string | null>(null);
  const [scrollVersion, setScrollVersion] = useState(0);
  const [isBusy, setIsBusy] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [, setStatusText] = useState('等待打开 Word 文档');
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<WordActionErrorState | null>(null);
  const [latestPlan, setLatestPlan] = useState<GoWordActionPlanResult | null>(null);
  const [failedImageRuns, setFailedImageRuns] = useState<Record<string, boolean>>({});
  const [draftParagraphTexts, setDraftParagraphTexts] = useState<Record<string, string>>({});
  const [focusedParagraphId, setFocusedParagraphId] = useState<string | null>(null);
  const paragraphEditorRef = useRef<HTMLTextAreaElement | null>(null);
  const skipDraftBlurCommitRef = useRef(false);

  useImperativeHandle(ref, () => ({
    saveDocument,
  }));

  useEffect(() => {
    onToolbarStateChange?.({
      canSave: Boolean(document) && document?.source !== 'mock',
      isBusy,
      isSaving,
      sessionId,
    });
  }, [document, isBusy, isSaving, onToolbarStateChange, sessionId]);

  const syncGoWordSession = (nextSessionId: string | null) => {
    sessionIdRef.current = nextSessionId;
    setSessionId(nextSessionId);
    publishWordEditorSession(nextSessionId);
  };

  const clearErrorState = useCallback(() => {
    setError(null);
    setActionError(null);
  }, []);

  const reportErrorState = useCallback((nextError: WordActionErrorState) => {
    setError(nextError.message);
    setActionError(nextError);
    if (nextError.plan) {
      setLatestPlan(nextError.plan);
    }
  }, []);

  const publishApplyIssue = useCallback((nextError: WordActionErrorState | null) => {
    if (!nextError) {
      publishWordEditorApplyError(null);
      return;
    }

    publishWordEditorApplyError({
      message: nextError.message,
      occurred_at: Date.now(),
      diagnostics: nextError.diagnostics ?? nextError.plan?.diagnostics ?? nextError.validation?.diagnostics,
      action: nextError.action ?? nextError.plan?.replay_action,
      plan: nextError.plan,
    });
  }, []);

  const resizeParagraphEditor = useCallback((textarea?: HTMLTextAreaElement | null) => {
    if (!textarea) {
      return;
    }

    textarea.style.height = '0px';
    textarea.style.height = `${Math.max(textarea.scrollHeight, 28)}px`;
  }, []);

  const openSessionFromDocument = async (doc: WordEditorDocumentState): Promise<{ id: string; document: GoWordDocument }> => {
    let path = doc.path ?? getAbsoluteFilePath(doc.file);
    if (!path && doc.file) {
      const uploaded = await uploadWordDocument(doc.file);
      path = uploaded.path;
    }
    if (!path) {
      throw new Error('无法解析本地文档路径，请使用桌面端打开或重新选择文件');
    }

    return openGoWordDocument(path);
  };

  const ensureActionSession = async (
    currentSnapshot: GoWordDocument,
    action: WordEditorActionPayload,
    currentDocument: WordEditorDocumentState | null,
  ): Promise<{ sessionId: string; snapshot: GoWordDocument }> => {
    const existingSessionId = sessionIdRef.current;
    if (existingSessionId) {
      return { sessionId: existingSessionId, snapshot: currentSnapshot };
    }

    const readerSessionId = action.reader_session_id?.trim();
    if (readerSessionId) {
      const restoredSession = await getGoWordDocument(readerSessionId);
      syncGoWordSession(restoredSession.id);
      setSnapshot(restoredSession.document);
      return { sessionId: restoredSession.id, snapshot: restoredSession.document };
    }

    if (currentDocument) {
      setStatusText('检测到会话缺失，正在自动重连 Word 文档...');
      const reopened = await openSessionFromDocument(currentDocument);
      syncGoWordSession(reopened.id);
      setSnapshot(reopened.document);
      return { sessionId: reopened.id, snapshot: reopened.document };
    }

    throw new Error('当前没有可执行的 go-word 会话，且无法自动重连文档');
  };

  const applyActionToSnapshot = async (
    currentSnapshot: GoWordDocument,
    action: WordEditorActionPayload,
    currentDocument: WordEditorDocumentState | null,
    forcedSessionId?: string,
    onPlanResolved?: (plan: GoWordActionPlanResult) => void,
  ): Promise<
    | { kind: 'noop'; statusText: string; activeParagraphId: string | null; replayAction: WordEditorActionPayload; plan: GoWordActionPlanResult }
    | { kind: 'preview'; statusText: string; activeParagraphId: string | null; replayAction: WordEditorActionPayload; plan: GoWordActionPlanResult }
    | {
      kind: 'applied';
      document: GoWordDocument;
      diffs: GoWordParagraphDiff[];
      activeParagraphId: string | null;
      statusText: string;
      replayAction: WordEditorActionPayload;
      validation: GoWordActionValidation;
      plan: GoWordActionPlanResult;
    }
  > => {
    const ensured = forcedSessionId
      ? { sessionId: forcedSessionId, snapshot: currentSnapshot }
      : await ensureActionSession(currentSnapshot, action, currentDocument);
    const currentSessionId = ensured.sessionId;
    const plan = await planGoWordAction(currentSessionId, action);
    if (plan.status === 'failed') {
      onPlanResolved?.(plan);
      throw new WordActionExecutionError(
        plan.diagnostics?.message || plan.status_text || '后端未能生成可安全执行的编辑计划',
        { diagnostics: plan.diagnostics, plan },
      );
    }

    if (plan.status === 'noop' || (plan.operations?.length ?? 0) === 0) {
      onPlanResolved?.(plan);
      return {
        kind: 'noop',
        statusText: plan.status_text ?? '无需执行额外修改',
        activeParagraphId: plan.active_paragraph_id ?? null,
        replayAction: plan.replay_action,
        plan,
      };
    }

    onPlanResolved?.(plan);
    if (action.preview_only) {
      return {
        kind: 'preview',
        statusText: `预览完成：${plan.status_text ?? '计划已生成'}`,
        activeParagraphId: plan.active_paragraph_id ?? null,
        replayAction: plan.replay_action,
        plan,
      };
    }

    const execution = await executeGoWordActionPlan(currentSessionId, action);
    if (execution.status === 'failed') {
      throw new WordActionExecutionError(
        execution.diagnostics?.message || execution.validation?.message || execution.status_text || '后端执行失败',
        {
          diagnostics: execution.diagnostics ?? execution.plan?.diagnostics ?? plan.diagnostics,
          validation: execution.validation,
          plan: execution.plan ?? plan,
        },
      );
    }
    if (execution.status === 'noop') {
      return {
        kind: 'noop',
        statusText: execution.status_text ?? '无需执行额外修改',
        activeParagraphId: execution.active_paragraph_id ?? null,
        replayAction: execution.replay_action,
        plan: execution.plan ?? plan,
      };
    }
    if (!execution.document) {
      throw new Error('后端执行结果缺少最新文档快照');
    }

    const resolvedParagraphId = execution.active_paragraph_id ?? null;
    const validation: GoWordActionValidation = {
      ok: execution.validation.ok,
      message: execution.validation.message ?? '自动验证未通过，请人工复核',
      error_code: execution.validation.error_code,
      diagnostics: execution.validation.diagnostics,
    };

    return {
      kind: 'applied',
      document: execution.document,
      diffs: execution.diffs ?? [],
      activeParagraphId: resolvedParagraphId,
      statusText: execution.status_text ?? plan.status_text ?? '已执行编辑计划',
      replayAction: execution.replay_action,
      validation,
      plan: execution.plan ?? plan,
    };
  };

  const replayRevisionsIntoSession = async (
    baseSessionId: string,
    baseSnapshot: GoWordDocument,
    sourceRevisions: WordRevision[],
    currentDocument: WordEditorDocumentState,
  ) => {
    let rebuiltSnapshot = baseSnapshot;
    const rebuiltRevisions: WordRevision[] = [];
    let lastActiveParagraphId: string | null = null;

    for (const revision of sourceRevisions) {
      const execution = await applyActionToSnapshot(rebuiltSnapshot, revision.replayAction, currentDocument, baseSessionId);
      if (execution.kind === 'applied') {
        rebuiltSnapshot = execution.document;
        lastActiveParagraphId = execution.activeParagraphId;
        rebuiltRevisions.push({
          ...revision,
          replayAction: execution.replayAction,
          activeParagraphId: execution.activeParagraphId,
          statusText: execution.statusText,
          diffs: execution.diffs ?? [],
        });
        continue;
      }

      lastActiveParagraphId = execution.activeParagraphId;
      rebuiltRevisions.push({
        ...revision,
        replayAction: execution.replayAction,
        activeParagraphId: execution.activeParagraphId,
        statusText: execution.statusText,
      });
    }

    return {
      snapshot: rebuiltSnapshot,
      revisions: rebuiltRevisions,
      activeParagraphId: lastActiveParagraphId,
    };
  };

  const handleKeepRevision = (revisionId: string) => {
    setRevisions((previous) => previous.map((revision) => (
      revision.id === revisionId
        ? { ...revision, decision: 'kept' }
        : revision
    )));
    setError(null);
    setStatusText('已保留当前修改，点击“保存”可写回文档');
  };

  const startInlineTextEditing = useCallback((paragraph: GoWordParagraph, isHeaderFooter: boolean) => {
    if (isHeaderFooter || isBusy || isSaving || document?.source === 'mock' || !canUseInlineTextEditing(paragraph)) {
      return;
    }

    setFocusedParagraphId(paragraph.id);
    setActiveParagraphId(paragraph.id);
    setDraftParagraphTexts((previous) => (
      previous[paragraph.id] === undefined
        ? { ...previous, [paragraph.id]: paragraphPlainText(paragraph) }
        : previous
    ));
  }, [document?.source, isBusy, isSaving]);

  const cancelInlineTextEditing = useCallback((paragraphId: string) => {
    setDraftParagraphTexts((previous) => {
      if (!(paragraphId in previous)) {
        return previous;
      }

      const next = { ...previous };
      delete next[paragraphId];
      return next;
    });
    setFocusedParagraphId((current) => (current === paragraphId ? null : current));
  }, []);

  const applyInlineTextEdit = useCallback(async (paragraph: GoWordParagraph) => {
    if (!snapshot || !document || document.source === 'mock') {
      return;
    }

    const paragraphId = paragraph.id;
    const nextText = draftParagraphTexts[paragraphId];
    const originalText = paragraphPlainText(paragraph);

    if (nextText === undefined || nextText === originalText) {
      cancelInlineTextEditing(paragraphId);
      return;
    }

    const directEditAction: WordEditorActionPayload = {
      action: 'replace',
      target_text: originalText,
      new_text: nextText,
      target_selector: {
        type: 'paragraph_id_selector',
        paragraph_id: paragraphId,
      },
      enable_track_changes: true,
      require_retrieval: false,
      context_scope: 'paragraph',
    };

    try {
      clearErrorState();
      setLatestPlan(null);
      publishWordEditorApplyError(null);
      setIsBusy(true);
      setStatusText('正在应用文本编辑...');

      const execution = await applyActionToSnapshot(snapshot, directEditAction, document, undefined, setLatestPlan);

      if (execution.kind === 'noop') {
        cancelInlineTextEditing(paragraphId);
        setActiveParagraphId(execution.activeParagraphId);
        setStatusText(execution.statusText);
        return;
      }

      if (execution.kind !== 'applied') {
        throw new Error('文本编辑未返回可应用结果');
      }

      setActiveParagraphId(execution.activeParagraphId);
      setScrollVersion((value) => value + 1);
      setSnapshot(execution.document);
      setRevisions((previous) => [...previous, {
        id: createRevisionId(Date.now()),
        action: directEditAction,
        replayAction: execution.replayAction,
        diffs: execution.diffs ?? [],
        activeParagraphId: execution.activeParagraphId,
        statusText: execution.statusText,
        decision: 'pending',
      }]);
      setWordUnsavedChanges(true);
      cancelInlineTextEditing(paragraphId);

      if (!execution.validation.ok) {
        const validationMessage = execution.validation.message ?? '自动验证未通过，请人工复核';
        setStatusText(execution.statusText || validationMessage);
        const validationError = {
          message: validationMessage,
          diagnostics: execution.validation.diagnostics,
          validation: execution.validation,
          action: directEditAction,
          plan: execution.plan,
        };
        reportErrorState(validationError);
        publishApplyIssue(validationError);
        return;
      }

      publishApplyIssue(null);
      setStatusText(`已应用文本编辑；${execution.validation.message}；修改已暂存，点击“保存”可写回文档`);
    } catch (applyError) {
      const nextError = toWordActionErrorState(applyError, '文本编辑失败', directEditAction);
      reportErrorState(nextError);
      publishApplyIssue(nextError);
      setStatusText('文本编辑失败');
    } finally {
      setIsBusy(false);
    }
  }, [cancelInlineTextEditing, clearErrorState, document, draftParagraphTexts, publishApplyIssue, reportErrorState, snapshot]);

  const handleRevertRevision = async (revisionId: string) => {
    if (!document || document.source === 'mock') {
      return;
    }

    const remainingRevisions = revisions.filter((revision) => revision.id !== revisionId);
    const previousSessionId = sessionIdRef.current;
    let reopenedSessionId: string | null = null;

    try {
      setIsBusy(true);
      clearErrorState();
      setStatusText('正在撤销所选修订并重建文档...');

      // 先关闭旧会话：后端 OpenDocument 会复用同路径已有会话（返回已修改状态），
      // 必须先从 sessions map 删除，才能从磁盘读取干净原始版本
      if (previousSessionId) {
        await closeGoWordSession(previousSessionId);
        syncGoWordSession(null);
      }

      const reopened = await openSessionFromDocument(document);
      reopenedSessionId = reopened.id;
      const rebuilt = await replayRevisionsIntoSession(reopened.id, reopened.document, remainingRevisions, document);

      syncGoWordSession(reopened.id);
      setSnapshot(rebuilt.snapshot);
      setRevisions(rebuilt.revisions);
      setLayoutSnapshot(null);

      const focusParagraphId = rebuilt.activeParagraphId;
      setActiveParagraphId(focusParagraphId);
      setScrollVersion((value) => value + 1);
      setStatusText(rebuilt.revisions.length > 0 ? '已撤销所选修订，其余修订已重新应用' : '已撤销最后一条修订，文档已恢复到保存状态');
    } catch (revertError) {
      if (reopenedSessionId) {
        void closeGoWordSession(reopenedSessionId);
      }
      reportErrorState(toWordActionErrorState(revertError, '撤销修订失败'));
      setStatusText('撤销修订失败');
    } finally {
      setIsBusy(false);
    }
  };

  const saveDocument = async (saveAs: boolean) => {
    if (document?.source === 'mock') {
      setStatusText('示例模式不支持保存，请先打开真实 docx');
      return false;
    }

    const resolveSaveAsPath = async (fallbackPath: string): Promise<string | null> => {
      const normalizedFallbackPath = fallbackPath.trim() || 'document.docx';

      if (window.electron?.showSaveDialog) {
        const selectedPath = await window.electron.showSaveDialog({
          title: '另存为 Word 文档',
          defaultPath: normalizedFallbackPath,
          filters: [{ name: 'Word 文档', extensions: ['docx', 'doc'] }],
        });
        return selectedPath?.trim() || null;
      }

      if (typeof window.prompt === 'function') {
        try {
          const inputPath = window.prompt('请输入另存为路径（支持绝对路径或工作区相对路径）', normalizedFallbackPath);
          return inputPath === null ? null : inputPath.trim();
        } catch {
          throw new Error('当前环境不支持另存为对话框');
        }
      }

      throw new Error('当前环境不支持另存为对话框');
    };

    try {
      setIsSaving(true);
      clearErrorState();

      let targetPath = '';
      if (saveAs) {
        const fallbackPath = snapshot?.source_path || document?.path || document?.name || 'document.docx';
        const inputPath = await resolveSaveAsPath(fallbackPath);
        if (inputPath === null) {
          setStatusText('已取消另存为');
          return false;
        }
        targetPath = inputPath;
      }

      let currentSessionId = sessionIdRef.current;
      if (!currentSessionId && document) {
        setStatusText(revisions.length > 0 ? '检测到会话缺失，正在重连并恢复修订...' : '检测到会话缺失，正在自动重连...');
        const reopened = await openSessionFromDocument(document);
        currentSessionId = reopened.id;
        const rebuiltSnapshot = revisions.length > 0
          ? (await replayRevisionsIntoSession(reopened.id, reopened.document, revisions, document)).snapshot
          : reopened.document;
        syncGoWordSession(reopened.id);
        setSnapshot(rebuiltSnapshot);
      }
      if (!currentSessionId) {
        throw new Error('当前没有可保存的会话');
      }

      const saved = await saveGoWordSession(currentSessionId, targetPath);
      setSnapshot(saved.document);
      setRevisions([]);
      setWordUnsavedChanges(false);
      setActiveParagraphId(null);
      setLayoutSnapshot(null);
      setStatusText(saveAs ? `已另存为：${saved.path}` : `已保存：${saved.path}`);
      return true;
    } catch (saveError) {
      const saveErrorMessage = saveError instanceof Error ? saveError.message : '保存失败';

      if (document && /session not found/i.test(saveErrorMessage)) {
        try {
          setStatusText(revisions.length > 0 ? '检测到会话失效，正在重连、恢复修订并重试保存...' : '检测到会话失效，正在自动重连并重试保存...');
          const reopened = await openSessionFromDocument(document);
          const rebuiltSnapshot = revisions.length > 0
            ? (await replayRevisionsIntoSession(reopened.id, reopened.document, revisions, document)).snapshot
            : reopened.document;
          syncGoWordSession(reopened.id);
          setSnapshot(rebuiltSnapshot);

          let targetPath = '';
          if (saveAs) {
            const fallbackPath = reopened.document.source_path || document.path || document.name || 'document.docx';
            const inputPath = await resolveSaveAsPath(fallbackPath);
            if (inputPath === null) {
              setStatusText('已取消另存为');
              return false;
            }
            targetPath = inputPath;
          }

          const saved = await saveGoWordSession(reopened.id, targetPath);
          setSnapshot(saved.document);
          setRevisions([]);
          setWordUnsavedChanges(false);
          setActiveParagraphId(null);
          setLayoutSnapshot(null);
          setStatusText(saveAs ? `已另存为：${saved.path}` : `已保存：${saved.path}`);
          return true;
        } catch (retryError) {
          const retryMessage = retryError instanceof Error ? retryError.message : '重连后保存仍失败';
          reportErrorState({ message: retryMessage });
          setStatusText('文档保存失败');
          return false;
        }
      }

      reportErrorState({ message: saveErrorMessage });
      setStatusText('文档保存失败');
      return false;
    } finally {
      setIsSaving(false);
    }
  };

  useEffect(() => {
    appliedActionVersionRef.current = actionVersion;
    const previousSessionId = sessionIdRef.current;
    syncGoWordSession(null);
    setRevisions([]);
    setActiveParagraphId(null);
    setFailedImageRuns({});
    setLayoutSnapshot(null);
    clearErrorState();

    if (previousSessionId) {
      void closeGoWordSession(previousSessionId);
    }

    if (!document) {
      setSnapshot(null);
      setStatusText('等待打开 Word 文档');
      setIsBusy(false);
      return;
    }

    if (document.source === 'mock') {
      setSnapshot(buildMockDocument(document.name));
      setStatusText('示例模式：可预览块结构，真实编辑请打开本地 docx');
      setIsBusy(false);
      return;
    }

    let cancelled = false;
    setSnapshot(null);
    setIsBusy(true);
    setStatusText('正在连接 go-word 会话...');

    const openDocument = async () => {
      try {
        const session = await openSessionFromDocument(document);
        if (cancelled) {
          await closeGoWordSession(session.id);
          return;
        }
        syncGoWordSession(session.id);
        setSnapshot(session.document);
        setStatusText(`已连接 go-word 会话 · 修订 ${session.document.revision}`);
      } catch (openError) {
        if (cancelled) {
          return;
        }
        setSnapshot(null);
        reportErrorState(toWordActionErrorState(openError, '打开 Word 文档失败'));
        setStatusText('文档打开失败');
      } finally {
        if (!cancelled) {
          setIsBusy(false);
        }
      }
    };

    void openDocument();

    return () => {
      cancelled = true;
      const currentSessionId = sessionIdRef.current;
      if (currentSessionId) {
        syncGoWordSession(null);
        void closeGoWordSession(currentSessionId);
      }
    };
  }, [document]);

  useEffect(() => {
    setLatestPlan(null);
  }, [document?.name, document?.path, document?.url, sessionId]);

  useEffect(() => {
    if (!lastAction || actionVersion === 0 || appliedActionVersionRef.current === actionVersion || !snapshot) {
      return;
    }

    const executeAction = async () => {
      const currentActionVersion = actionVersion;
      const currentAction = lastAction;
      const currentSnapshot = snapshot;
      appliedActionVersionRef.current = currentActionVersion;

      if (document?.source === 'mock') {
        if (currentAction.action === 'scroll') {
          const paragraph = locateParagraph(currentSnapshot, currentAction.target_text);
          setActiveParagraphId(paragraph?.id ?? null);
          setScrollVersion((value) => value + 1);
          setStatusText(paragraph ? '示例文档已定位到目标段落' : '示例文档未找到目标段落');
          return;
        }
        setStatusText('示例模式不执行真实写入，请打开本地 docx');
        return;
      }

      try {
        clearErrorState();
        setLatestPlan(null);
        publishWordEditorApplyError(null);
        setIsBusy(true);
        setStatusText(`正在执行 ${currentAction.action} 操作...`);
        const execution = await applyActionToSnapshot(currentSnapshot, currentAction, document ?? null, undefined, setLatestPlan);

        if (execution.kind === 'noop' || execution.kind === 'preview') {
          setActiveParagraphId(execution.activeParagraphId);
          setScrollVersion((value) => value + 1);
          setStatusText(execution.statusText);
          return;
        }

        setActiveParagraphId(execution.activeParagraphId);
        setScrollVersion((value) => value + 1);
        setSnapshot(execution.document);
        setRevisions((previous) => [...previous, {
          id: createRevisionId(currentActionVersion),
          action: currentAction,
          replayAction: execution.replayAction,
          diffs: execution.diffs,
          activeParagraphId: execution.activeParagraphId,
          statusText: execution.statusText,
          decision: 'pending',
        }]);
        setWordUnsavedChanges(true);
		if (!execution.validation.ok) {
      const validationMessage = execution.validation.message ?? '自动验证未通过，请人工复核';
      setStatusText(execution.statusText || validationMessage);
			const validationError = {
        message: validationMessage,
			  diagnostics: execution.validation.diagnostics,
			  validation: execution.validation,
        action: currentAction,
        plan: execution.plan,
			};
			reportErrorState(validationError);
      publishApplyIssue(validationError);
          return;
		}
    publishApplyIssue(null);

        setStatusText(`${execution.statusText}；${execution.validation.message}；修改已暂存，点击“保存”可写回文档`);
      } catch (applyError) {
      const nextError = toWordActionErrorState(applyError, '执行 Word 编辑动作失败', currentAction);
      reportErrorState(nextError);
      publishApplyIssue(nextError);
        setStatusText('文档操作失败');
      } finally {
        setIsBusy(false);
      }
    };

    void executeAction();
  }, [actionVersion, clearErrorState, document?.source, lastAction, publishApplyIssue, reportErrorState, snapshot]);

  useEffect(() => {
    setLayoutSnapshot(null);
  }, [document?.source, sessionId, snapshot]);

  useEffect(() => {
    setFailedImageRuns({});
  }, [sessionId]);

  useEffect(() => {
    setDraftParagraphTexts({});
    setFocusedParagraphId(null);
  }, [document?.name, document?.path, sessionId]);

  useEffect(() => {
    if (!focusedParagraphId) {
      return;
    }

    const textarea = paragraphEditorRef.current;
    if (!textarea) {
      return;
    }

    textarea.focus();
    textarea.setSelectionRange(textarea.value.length, textarea.value.length);
    resizeParagraphEditor(textarea);
  }, [focusedParagraphId, resizeParagraphEditor]);

  useEffect(() => {
    if (!activeParagraphId || !containerRef.current) {
      return;
    }

    let disposed = false;
    let animationFrameId = 0;
    let retryCount = 0;
    const maxRetries = 12;

    const scrollIntoActiveParagraph = (): boolean => {
      const container = containerRef.current;
      if (!container) {
        return false;
      }

      const target = container.querySelector(`[data-paragraph-id="${activeParagraphId}"]`);
      if (!(target instanceof HTMLElement)) {
        return false;
      }

      const containerRect = container.getBoundingClientRect();
      const targetRect = target.getBoundingClientRect();
      const nextTop = container.scrollTop + (targetRect.top - containerRect.top) - (container.clientHeight / 2) + (targetRect.height / 2);
      container.scrollTo({
        top: Math.max(0, nextTop),
        behavior: 'auto',
      });
      return true;
    };

    const scheduleRetry = () => {
      if (disposed || retryCount >= maxRetries) {
        return;
      }
      retryCount += 1;
      animationFrameId = window.requestAnimationFrame(() => {
        if (!scrollIntoActiveParagraph()) {
          scheduleRetry();
        }
      });
    };

    const observer = new MutationObserver(() => {
      if (scrollIntoActiveParagraph()) {
        observer.disconnect();
        window.cancelAnimationFrame(animationFrameId);
      }
    });

    observer.observe(containerRef.current, { childList: true, subtree: true });

    if (!scrollIntoActiveParagraph()) {
      scheduleRetry();
    }

    return () => {
      disposed = true;
      window.cancelAnimationFrame(animationFrameId);
      observer.disconnect();
    };
  }, [activeParagraphId, scrollVersion]);

  const measurementRef = useRef<HTMLDivElement>(null);
  const viewportStableKeyRef = useRef<string | null>(null);
  const [pageLayouts, setPageLayouts] = useState<PagePreviewLayout[]>([]);
  const [viewportWidth, setViewportWidth] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(0);
  const [isViewportStable, setIsViewportStable] = useState(false);
  const measureViewport = useCallback(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    const nextWidth = container.clientWidth;
    const nextHeight = container.clientHeight;

    setViewportWidth((previous) => (previous === nextWidth ? previous : nextWidth));
    setViewportHeight((previous) => (previous === nextHeight ? previous : nextHeight));
  }, []);
  const diffMap = useMemo(() => {
    const nextMap = new Map<string, GoWordParagraphDiff>();
    revisions.filter((revision) => revision.decision === 'pending').forEach((revision) => {
      revision.diffs.forEach((diff) => {
        nextMap.set(diff.paragraph_id, diff);
      });
    });
    return nextMap;
  }, [revisions]);
  const paragraphRevisionSummaries = useMemo(() => {
    const nextMap = new Map<string, ParagraphRevisionSummary[]>();
    revisions.filter((revision) => revision.decision === 'pending').forEach((revision) => {
      revision.diffs.forEach((diff) => {
        const entries = nextMap.get(diff.paragraph_id) ?? [];
        changeSummary(diff).forEach((item, index) => {
          entries.push({
            id: `${revision.id}-${diff.paragraph_id}-${index}`,
            revisionId: revision.id,
            decision: revision.decision,
            diff: item,
          });
        });
        nextMap.set(diff.paragraph_id, entries);
      });
    });
    return nextMap;
  }, [revisions]);
  const snapshotParagraphIds = useMemo(() => {
    const ids = new Set<string>();
    (snapshot?.paragraphs ?? []).forEach((paragraph) => {
      if (paragraph.id) {
        ids.add(paragraph.id);
      }
    });
    return ids;
  }, [snapshot]);
  const orphanDeletedRevisionEntries = useMemo(() => {
    const entries: DeletedRevisionInlineEntry[] = [];
    revisions.filter((revision) => revision.decision === 'pending').forEach((revision) => {
      revision.diffs.forEach((diff) => {
        const paragraphId = (diff.paragraph_id ?? '').trim();
        if (diff.type !== 'deleted' || (paragraphId && snapshotParagraphIds.has(paragraphId))) {
          return;
        }
        const anchorParagraphId = revision.activeParagraphId && snapshotParagraphIds.has(revision.activeParagraphId)
          ? revision.activeParagraphId
          : null;
        changeSummary(diff).forEach((item, index) => {
          if (item.type !== 'deleted') {
            return;
          }
          entries.push({
            id: `${revision.id}-${paragraphId || 'orphan'}-${index}`,
            revisionId: revision.id,
            decision: revision.decision,
            diff: item,
            anchorParagraphId,
          });
        });
      });
    });
    return entries;
  }, [revisions, snapshotParagraphIds]);
  const orphanDeletedByAnchor = useMemo(() => {
    const anchored = new Map<string, DeletedRevisionInlineEntry[]>();
    const trailing: DeletedRevisionInlineEntry[] = [];
    orphanDeletedRevisionEntries.forEach((entry) => {
      if (!entry.anchorParagraphId) {
        trailing.push(entry);
        return;
      }
      const items = anchored.get(entry.anchorParagraphId) ?? [];
      items.push(entry);
      anchored.set(entry.anchorParagraphId, items);
    });
    return { anchored, trailing };
  }, [orphanDeletedRevisionEntries]);
  const liveDraftDiffMap = useMemo(() => {
    const nextMap = new Map<string, GoWordParagraphDiff>();
    if (!snapshot) {
      return nextMap;
    }

    Object.entries(draftParagraphTexts).forEach(([paragraphId, draftText]) => {
      const paragraph = locateParagraphById(snapshot, paragraphId);
      if (!paragraph) {
        return;
      }

      const diff = buildLiveParagraphDiff(paragraphId, paragraphPlainText(paragraph), draftText);
      if (diff) {
        nextMap.set(paragraphId, diff);
      }
    });

    return nextMap;
  }, [draftParagraphTexts, snapshot]);
  const renderBlocks = useMemo(
    () => (snapshot?.blocks && snapshot.blocks.length > 0
      ? snapshot.blocks
      : snapshot?.paragraphs.map((paragraph) => ({ type: 'paragraph' as const, paragraph }))) ?? [],
    [snapshot],
  );
  const documentSections = useMemo(() => resolveDocumentSections(snapshot), [snapshot]);
  const previewItems = useMemo(() => buildPreviewFlowItems(renderBlocks, documentSections), [documentSections, renderBlocks]);
  const backendPageLayouts = useMemo(
    () => buildPageLayoutsFromBackendLayout(layoutSnapshot, previewItems, documentSections),
    [documentSections, layoutSnapshot, previewItems],
  );
  const defaultPageLayout = useMemo(() => resolvePageLayout(snapshot?.meta?.section_layout), [snapshot?.meta?.section_layout]);
  const sectionMetrics = useMemo(() => {
    const sourceSections = documentSections.length > 0 ? documentSections : [{ layout: snapshot?.meta?.section_layout }];
    return sourceSections.map((section) => {
      const layout = resolvePageLayout(section.layout ?? snapshot?.meta?.section_layout);
      return {
        section,
        layout,
        pageWidthPx: twipsToPx(layout.pageWidth) ?? 794,
        pageHeightPx: twipsToPx(layout.pageHeight) ?? 1122,
        marginTopPx: twipsToPx(layout.marginTop) ?? 96,
        marginRightPx: twipsToPx(layout.marginRight) ?? 120,
        marginBottomPx: twipsToPx(layout.marginBottom) ?? 96,
        marginLeftPx: twipsToPx(layout.marginLeft) ?? 120,
        marginHeaderPx: twipsToPx(layout.marginHeader) ?? 48,
        marginFooterPx: twipsToPx(layout.marginFooter) ?? 48,
        pageContentWidthPx: pageContentWidthFromLayout(layout),
        pageContentHeightPx: pageContentHeightFromLayout(layout),
      };
    });
  }, [documentSections, snapshot?.meta?.section_layout]);
  const maxPageWidthPx = sectionMetrics.reduce((maxValue, metrics) => Math.max(maxValue, metrics.pageWidthPx), twipsToPx(defaultPageLayout.pageWidth) ?? 794);
  const maxPageHeightPx = sectionMetrics.reduce((maxValue, metrics) => Math.max(maxValue, metrics.pageHeightPx), twipsToPx(defaultPageLayout.pageHeight) ?? 1122);
  const pageViewportInsetPx = 32;
  const availableViewportWidth = Math.max(viewportWidth - pageViewportInsetPx, 320);
  const availableViewportHeight = Math.max(viewportHeight - pageViewportInsetPx, 320);
  const effectivePageLayouts = backendPageLayouts.length > 0 ? backendPageLayouts : pageLayouts;
  const totalPages = effectivePageLayouts.length > 0 ? effectivePageLayouts.length : (previewItems.length > 0 ? 1 : 0);
  const pageNumberContexts = useMemo(
    () => buildPageNumberContexts(effectivePageLayouts.map((page) => page.section)),
    [effectivePageLayouts],
  );
  const widthScale = viewportWidth > 0 ? Math.min(1, availableViewportWidth / maxPageWidthPx) : 1;
  const heightScale = viewportHeight > 0 ? Math.min(1, availableViewportHeight / maxPageHeightPx) : 1;
  const pageScale = totalPages <= 1 ? Math.min(widthScale, heightScale) : widthScale;
  const scaledPageWidthPx = Math.round(maxPageWidthPx * pageScale);
  const scaledPageHeightPx = Math.round(maxPageHeightPx * pageScale);
  const previewDocumentKey = document?.source ?? document?.path ?? document?.name ?? null;

  useLayoutEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }

    measureViewport();
    const observer = new ResizeObserver(() => {
      measureViewport();
    });
    observer.observe(container);
    window.addEventListener('resize', measureViewport);

    return () => {
      observer.disconnect();
      window.removeEventListener('resize', measureViewport);
    };
  }, [measureViewport]);

  useLayoutEffect(() => {
    if (!document) {
      setIsViewportStable(false);
      viewportStableKeyRef.current = null;
      return;
    }

    let frameId = 0;
    let nestedFrameId = 0;
    const currentStableKey = previewDocumentKey;
    if (viewportStableKeyRef.current !== currentStableKey) {
      setIsViewportStable(false);
      viewportStableKeyRef.current = currentStableKey;
    }
    const finalizeMeasurement = () => {
      const container = containerRef.current;
      if (!container) {
        return;
      }

      if (container.clientWidth > 0 && container.clientHeight > 0) {
        setIsViewportStable(true);
      }
    };
    const timeoutId = window.setTimeout(() => {
      measureViewport();
      finalizeMeasurement();
    }, 160);

    measureViewport();
    frameId = window.requestAnimationFrame(() => {
      measureViewport();
      nestedFrameId = window.requestAnimationFrame(() => {
        measureViewport();
        finalizeMeasurement();
      });
    });

    return () => {
      window.cancelAnimationFrame(frameId);
      window.cancelAnimationFrame(nestedFrameId);
      window.clearTimeout(timeoutId);
    };
  }, [document, measureViewport, previewDocumentKey]);

  useLayoutEffect(() => {
	setPageLayouts([]);
  }, [previewItems]);

  const renderRun = (
    run: GoWordRun,
    paragraphDiff?: GoWordParagraphDiff,
    globalOffset = 0,
    pageContext?: PageRenderContext,
  ) => {
    if (run.break_type === 'line' || run.break_type === 'textWrapping') {
      return <br key={run.id} />;
    }
    if (run.break_type === 'page' || run.break_type === 'renderedPage') {
      return null;
    }
    if (run.image) {
      const widthPx = run.image.width_emu ? Math.max(24, Math.round(run.image.width_emu / 9525)) : undefined;
      const heightPx = run.image.height_emu ? Math.max(24, Math.round(run.image.height_emu / 9525)) : undefined;
      const canRequestImage = Boolean(sessionId && (run.image.relationship_id || run.image.target));
      const imageURL = canRequestImage
        ? buildGoWordImageURL(sessionId!, run.image.relationship_id, run.image.target)
        : '';
      const isFailed = failedImageRuns[run.id] === true;

      if (canRequestImage && !isFailed) {
        return (
          <span key={run.id} className="mx-1 inline-flex align-middle">
            <img
              src={imageURL}
              alt={run.image.name || run.image.filename || '文档内嵌图片'}
              className="rounded border border-[#d8cbbd] bg-[#fffdf8] object-contain shadow-sm"
              style={{
                width: widthPx ? `${widthPx}px` : 'auto',
                height: heightPx ? `${heightPx}px` : 'auto',
                maxWidth: '100%',
                maxHeight: '360px',
              }}
              onError={() => {
                setFailedImageRuns((previous) => ({ ...previous, [run.id]: true }));
              }}
            />
          </span>
        );
      }

      return (
        <span key={run.id} className="mx-1 inline-flex items-center gap-1 rounded-md border border-[#d8cbbd] bg-[#f7f1e8] px-2 py-1 align-middle text-[11px] text-[#7a5e47]">
          <FileImage className="h-3.5 w-3.5" />
          <span>{run.image.name || run.image.filename || '内嵌图片'}</span>
        </span>
      );
    }

    const rawText = run.text ?? '';
    const text = pageContext?.isHeaderFooter
      ? applyPageNumberText(rawText, pageContext.displayedPageNumber, pageContext.totalPages, pageContext.pageNumberFormat)
      : rawText;
    if (!text) {
      return null;
    }

    const ranges = buildHighlightRanges(paragraphDiff);
    const segments = splitTextSegments(text, globalOffset, ranges);
    const color = normalizeColor(run.style.color);
    const fontSizePt = run.style.font_size_hp ? run.style.font_size_hp / 2 : undefined;

    return (
      <React.Fragment key={run.id}>
        {segments.map((segment, index) => (
          <React.Fragment key={`${run.id}-${index}`}>
            {!pageContext?.isHeaderFooter && segment.deletedText && (
              <span
                className="rounded-sm bg-[#fde2e2] text-[#9b3838]"
                style={{
                  textDecoration: 'line-through',
                }}
              >
                {segment.deletedText}
              </span>
            )}
            <span
              className={cn(
                !pageContext?.isHeaderFooter && segment.type === 'inserted' && 'rounded-sm bg-[#ddf6dc]',
                !pageContext?.isHeaderFooter && segment.type === 'updated' && 'rounded-sm bg-[#fde7b1]',
              )}
              style={{
                fontWeight: run.style.bold ? 700 : 400,
                fontStyle: run.style.italic ? 'italic' : 'normal',
                textDecoration: run.style.underline ? 'underline' : 'none',
                color,
                fontFamily: run.style.font_family || undefined,
                fontSize: fontSizePt ? `${fontSizePt}pt` : undefined,
              }}
            >
              {segment.text}
            </span>
          </React.Fragment>
        ))}
      </React.Fragment>
    );
  };

  const renderParagraph = (paragraph: GoWordParagraph, pageContext?: PageRenderContext, isMeasurement = false) => {
    const isHeaderFooter = pageContext?.isHeaderFooter === true;
    const liveDiff = isHeaderFooter ? undefined : liveDraftDiffMap.get(paragraph.id);
    const diff = liveDiff ?? (isHeaderFooter ? undefined : diffMap.get(paragraph.id));
    const paragraphStyle = buildParagraphStyle(paragraph);
    const numberingPrefix = !isHeaderFooter
      ? (paragraph.numbering_text ? `${paragraph.numbering_text} ` : (paragraph.style.numbering ? '• ' : ''))
      : '';
    const revisionSummaryItems = isHeaderFooter ? [] : (paragraphRevisionSummaries.get(paragraph.id) ?? []);
    const draftText = draftParagraphTexts[paragraph.id];
    const isFocused = !isMeasurement && focusedParagraphId === paragraph.id;
    const canEdit = !isMeasurement && !isHeaderFooter && document?.source !== 'mock' && canUseInlineTextEditing(paragraph);
    let globalOffset = 0;

    return (
      <div
        key={paragraph.id}
        data-paragraph-id={paragraph.id}
        className={cn(
          'rounded-sm px-1 py-[2px]',
          canEdit && 'cursor-text transition hover:bg-[#f7efe2]',
          !isHeaderFooter && !isMeasurement && diff?.type === 'inserted' && 'bg-[#eef9ee]',
          !isHeaderFooter && !isMeasurement && diff?.type === 'updated' && 'bg-[#fff7df]',
        )}
        onClick={() => {
          if (canEdit && !isFocused) {
            startInlineTextEditing(paragraph, isHeaderFooter);
          }
        }}
      >
        {isFocused && draftText !== undefined ? (
          <div className="flex items-start gap-1 rounded-sm bg-white/90">
            {numberingPrefix && <span className="pt-[2px]">{numberingPrefix}</span>}
            <textarea
              ref={paragraphEditorRef}
              value={draftText}
              onChange={(event) => {
                setDraftParagraphTexts((previous) => ({
                  ...previous,
                  [paragraph.id]: event.target.value,
                }));
                resizeParagraphEditor(event.target);
              }}
              onBlur={() => {
                if (skipDraftBlurCommitRef.current) {
                  skipDraftBlurCommitRef.current = false;
                  return;
                }
                void applyInlineTextEdit(paragraph);
              }}
              onKeyDown={(event) => {
                if (event.key === 'Escape') {
                  skipDraftBlurCommitRef.current = true;
                  event.preventDefault();
                  cancelInlineTextEditing(paragraph.id);
                  return;
                }
                if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
                  skipDraftBlurCommitRef.current = true;
                  event.preventDefault();
                  void applyInlineTextEdit(paragraph);
                }
              }}
              className="w-full resize-none overflow-hidden border-0 bg-transparent p-0 text-[#3d2b1f] outline-none"
              style={paragraphStyle}
            />
          </div>
        ) : (
          <>
            <div data-measure-paragraph-text={isMeasurement ? 'true' : undefined} className="whitespace-pre-wrap break-words leading-[1.7] text-[#3d2b1f]" style={paragraphStyle}>
              {numberingPrefix && <span>{numberingPrefix}</span>}
              {paragraph.runs.length > 0 ? paragraph.runs.map((run) => {
                const runNode = renderRun(run, diff, globalOffset, pageContext);
                globalOffset += (run.text ?? '').length;
                return runNode;
              }) : <span>&nbsp;</span>}
            </div>
          </>
        )}
        {!isHeaderFooter && !isMeasurement && !liveDiff && revisionSummaryItems.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-2 text-[11px] text-[#6c5845]">
            {revisionSummaryItems.map((item) => (
              <div
                key={item.id}
                className={cn(
                  'inline-flex items-center gap-1 rounded-full px-2 py-1',
                  item.diff.type === 'deleted' && 'bg-[#fde2e2] text-[#9b3838]',
                  item.diff.type === 'inserted' && 'bg-[#dcf7df] text-[#2f6b3f]',
                  item.diff.type === 'updated' && 'bg-[#fdf0c5] text-[#8a6120]',
                )}
              >
                <span>{formatSummaryItem(item.diff)}</span>
                {item.decision === 'kept' ? (
                  <span className="rounded-full bg-white/55 px-1.5 py-0.5 text-[10px]">已保留</span>
                ) : (
                  <button
                    type="button"
                    onClick={() => {
                      handleKeepRevision(item.revisionId);
                    }}
                    disabled={isBusy || isSaving}
                    className="rounded-full bg-white/70 px-1.5 py-0.5 text-[10px] transition hover:bg-white disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    保留
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => {
                    void handleRevertRevision(item.revisionId);
                  }}
                  disabled={isBusy || isSaving}
                  className="rounded-full bg-white/70 px-1.5 py-0.5 text-[10px] transition hover:bg-white disabled:cursor-not-allowed disabled:opacity-50"
                >
                  撤销
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    );
  };

  const renderParagraphFragment = (item: ParagraphFragmentPageItem, pageContext?: PageRenderContext) => {
    const fragmentHeight = Math.max(item.fragmentHeight, 1);

    return (
      <div key={item.key} style={{ height: `${fragmentHeight}px`, overflow: 'hidden' }}>
        <div style={{ transform: `translateY(-${item.offsetTop}px)` }}>
          {renderParagraph(item.paragraph, pageContext, false)}
        </div>
      </div>
    );
  };

  const renderDeletedInlineEntry = (entry: DeletedRevisionInlineEntry, key: string) => {
    const deletedText = (entry.diff.before_text ?? '').trim();
    return (
      <div key={key} className="rounded-sm px-1 py-[2px]">
        <div className="whitespace-pre-wrap break-words leading-[1.7] text-[#9b3838]">
          <span className="rounded-sm bg-[#fde2e2]" style={{ textDecoration: 'line-through' }}>
            {deletedText || '已删除内容'}
          </span>
        </div>
        <div className="mt-1 flex flex-wrap gap-2 text-[11px] text-[#6c5845]">
          {entry.decision === 'kept' ? (
            <span className="inline-flex items-center rounded-full bg-white/65 px-2 py-1 text-[10px]">已保留</span>
          ) : (
            <button
              type="button"
              onClick={() => {
                handleKeepRevision(entry.revisionId);
              }}
              disabled={isBusy || isSaving}
              className="rounded-full bg-white/70 px-2 py-1 text-[10px] transition hover:bg-white disabled:cursor-not-allowed disabled:opacity-50"
            >
              保留
            </button>
          )}
          <button
            type="button"
            onClick={() => {
              void handleRevertRevision(entry.revisionId);
            }}
            disabled={isBusy || isSaving}
            className="rounded-full bg-white/70 px-2 py-1 text-[10px] transition hover:bg-white disabled:cursor-not-allowed disabled:opacity-50"
          >
            撤销
          </button>
        </div>
      </div>
    );
  };

  const renderTable = (
    table: GoWordTable,
    key: string,
    pageContext?: PageRenderContext,
    isMeasurement = false,
    rowIndexes?: number[],
    rowClips?: Record<number, { start: number; height: number }>,
  ): React.ReactNode => {
    const rowsToRender = rowIndexes
      ? rowIndexes.map((rowIndex) => ({ row: table.rows[rowIndex], rowIndex }))
      : table.rows.map((row, rowIndex) => ({ row, rowIndex }));

    // 从 grid_xml 解析列宽（twips → px），优先使用 Word 原始列宽布局。
    const gridColTwips = parseTableGridColTwips(table.grid_xml);
    const colWidthsPx = gridColTwips
      .map((twips) => twipsToPx(twips) ?? 0)
      .filter((width) => width > 0);
    const hasColLayout = colWidthsPx.length > 0 && colWidthsPx.length === gridColTwips.length;
    const tableTotalWidthPx = hasColLayout ? colWidthsPx.reduce((a, b) => a + b, 0) : 0;
    const colWidthsPercent = hasColLayout && tableTotalWidthPx > 0
      ? colWidthsPx.map((width) => (width / tableTotalWidthPx) * 100)
      : [];

    return (
      <div
        key={key}
        data-measure-table-root={isMeasurement ? 'true' : undefined}
        className={cn(
          'overflow-hidden border border-[#d8cbbd] bg-[#fffdfa]',
          'shadow-[0_8px_24px_rgba(97,68,45,0.08)]', // 统一无圆角
        )}
      >
        <table
          className={cn(
            'border-collapse text-left text-[14px] text-[#3d2b1f]',
            'w-full',
          )}
          style={hasColLayout ? { tableLayout: 'fixed' } : undefined}
        >
          {hasColLayout && (
            <colgroup>
              {colWidthsPercent.map((widthPercent, index) => (
                <col key={`col-${index}`} style={{ width: `${widthPercent}%` }} />
              ))}
            </colgroup>
          )}
          <tbody>
            {rowsToRender.map(({ row, rowIndex }, renderIndex) => (
              <tr
                key={`${row.id}-${renderIndex}`}
                data-measure-table-row-index={isMeasurement ? rowIndex : undefined}
                className={cn(
                  'border-b border-[#eadfce] last:border-b-0',
                  row.repeat_header && 'bg-[#f6ede1]',
                )}
              >
                {row.cells.map((cell) => {
                  const clip = rowClips?.[rowIndex];
                  const cellColSpan = parseCellGridSpan(cell.properties_xml);
                  return (
                  <td
                    key={`${cell.id}-${renderIndex}`}
                    colSpan={cellColSpan > 1 ? cellColSpan : undefined}
                    className={cn(
                      'align-top border-r border-[#eadfce] last:border-r-0',
                      clip ? 'px-0 py-0' : 'px-3 py-2',
                    )}
                  >
                    {rowClips?.[rowIndex] ? (
                      <div style={{ height: `${clip!.height}px`, overflow: 'hidden' }}>
                        <div className="space-y-2 px-3 py-2" style={{ transform: `translateY(-${clip!.start}px)` }}>
                          {(cell.blocks ?? []).map((childBlock, index) => renderBlock(childBlock, `${cell.id}-${renderIndex}-${index}`, pageContext, isMeasurement))}
                        </div>
                      </div>
                    ) : (
                      <div className="space-y-2">
                        {(cell.blocks ?? []).map((childBlock, index) => renderBlock(childBlock, `${cell.id}-${renderIndex}-${index}`, pageContext, isMeasurement))}
                      </div>
                    )}
                  </td>
                );})}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  };

  const renderBlock = (block: GoWordBlock, key: string, pageContext?: PageRenderContext, isMeasurement = false): React.ReactNode => {
    if (block.type === 'table' && block.table) {
      return renderTable(block.table, key, pageContext, isMeasurement);
    }

    if (block.paragraph) {
      return <React.Fragment key={key}>{renderParagraph(block.paragraph, pageContext, isMeasurement)}</React.Fragment>;
    }

    return null;
  };

  const renderPageItem = (item: PageRenderItem, pageContext?: PageRenderContext): React.ReactNode => {
    if (item.kind === 'table-fragment') {
      return renderTable(item.table, item.key, pageContext, false, item.rowIndexes, item.rowClips);
    }
    if (item.kind === 'paragraph-fragment') {
      return renderParagraphFragment(item, pageContext);
    }
    return renderBlock(item.block, item.key, pageContext, false);
  };

  const renderHeaderFooterSection = (part: GoWordHeaderFooterPart | null, kind: 'header' | 'footer', pageContext: PageRenderContext) => {
    if (!part) {
      return null;
    }
    const blocks = part.blocks && part.blocks.length > 0
      ? part.blocks
      : (part.paragraphs ?? []).map((paragraph) => ({ type: 'paragraph' as const, paragraph }));
    if (blocks.length === 0) {
      return null;
    }
    return (
      <div className="w-full space-y-1">
        {blocks.map((block, blockIndex) => renderBlock(block, `${kind}-${pageContext.pageNumber}-${blockIndex}`, { ...pageContext, isHeaderFooter: true }))}
      </div>
    );
  };

  const continuousPageContext: PageRenderContext = {
    pageNumber: 1,
    displayedPageNumber: 1,
    sectionPageOrdinal: 1,
    pageNumberFormat: documentSections[0]?.layout?.page_number_format,
    totalPages: 1,
  };
  const continuousPageNumberContext: PageNumberContext = {
    globalPageNumber: 1,
    sectionPageOrdinal: 1,
    displayedPageNumber: 1,
    totalPages: 1,
  };
  const continuousHeaderFooterParts = documentSections[0]?.header_footers ?? snapshot?.meta?.header_footers ?? [];
  const continuousHeaderPart = pickHeaderFooterPart(continuousHeaderFooterParts, 'header', continuousPageNumberContext, documentSections[0]?.layout);
  const continuousFooterPart = pickHeaderFooterPart(continuousHeaderFooterParts, 'footer', continuousPageNumberContext, documentSections[0]?.layout);
  const continuousViewportInsetPx = 48;
  const paperFrameBorderPx = 2;
  const continuousBasePaperWidthPx = maxPageWidthPx;
  const continuousAvailableWidthPx = viewportWidth > 0
    ? Math.max(320, viewportWidth - continuousViewportInsetPx - paperFrameBorderPx)
    : continuousBasePaperWidthPx;
  const continuousPaperScale = continuousBasePaperWidthPx > 0
    ? Math.min(1, continuousAvailableWidthPx / continuousBasePaperWidthPx)
    : 1;
  const continuousScaledPaperWidthPx = Math.max(320, Math.round(continuousBasePaperWidthPx * continuousPaperScale));
  const canRenderPreview = Boolean(snapshot && previewItems.length > 0 && isViewportStable);
  const candidateDiagnostics = actionError?.diagnostics ?? actionError?.plan?.diagnostics;
  const previewPlaceholderText = snapshot && previewItems.length > 0
    ? '正在稳定文档视图...'
    : isBusy
      ? '正在加载文档内容...'
      : '当前没有可渲染的 Word 快照';

  if (!document) {
    return (
      <div className="h-full bg-[#efe7dc]" />
    );
  }

  return (
    <div className="flex h-full flex-col bg-[#e7ddd0]">
      <div className="border-b border-[#e0d4c6] bg-[#f8f2e9] px-4 py-3">
        <div className="flex items-center justify-end gap-3">
          <div className="flex items-center gap-3 text-xs text-[#7f6a57]">
            {(isBusy || isSaving) && <Loader2 className="h-4 w-4 animate-spin" />}
          </div>
        </div>
        {error && (
          <div className="mt-3 flex items-start gap-2 rounded-lg border border-[#f1c2c2] bg-[#fff0f0] px-3 py-2 text-xs text-[#9b3838]">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <div className="min-w-0 flex-1 space-y-2">
              <div>{error}</div>
              {actionError?.diagnostics?.error_code && (
                <div className="text-[11px] text-[#a55a5a]">
                  错误码：{actionError.diagnostics.error_code}
                </div>
              )}
              {actionError?.diagnostics?.recommended_actions && actionError.diagnostics.recommended_actions.length > 0 && (
                <div className="rounded-md border border-[#f4d4d4] bg-white/60 px-2 py-2 text-[11px] text-[#7f4a4a]">
                  <div className="mb-1 font-medium text-[#8d4444]">建议下一步</div>
                  <div className="space-y-1">
                    {actionError.diagnostics.recommended_actions.map((item, index) => (
                      <div key={`${item}-${index}`}>• {item}</div>
                    ))}
                  </div>
                </div>
              )}
              {actionError?.diagnostics?.attempted_strategies && actionError.diagnostics.attempted_strategies.length > 0 && (
                <div className="text-[11px] text-[#8a6464]">
                  已尝试策略：{actionError.diagnostics.attempted_strategies.join('、')}
                </div>
              )}
              {candidateDiagnostics?.candidates && candidateDiagnostics.candidates.length > 0 && (
                <div className="rounded-md border border-[#f4d4d4] bg-white/60 px-2 py-2 text-[11px] text-[#7f4a4a]">
                  <div className="mb-1 font-medium text-[#8d4444]">
                    定位存在歧义，已回退给 Agent 重新定位{typeof candidateDiagnostics.candidate_count === 'number' ? `（共 ${candidateDiagnostics.candidate_count} 个候选）` : ''}
                  </div>
                  <div className="mb-2 text-[11px] text-[#8a6464]">
                    系统会先自行重新阅读文档并重定位，不再要求人工逐个确认候选。
                  </div>
                  <div className="space-y-1.5">
                    {candidateDiagnostics.candidates.slice(0, 3).map((candidate, index) => (
                      <div key={`${candidate.paragraph_id || candidate.table_id || 'candidate'}-${index}`} className="rounded border border-[#efd9d9] bg-white/85 px-2 py-1.5">
                        <div className="font-medium text-[#744141]">{candidate.preview || candidate.matched_text || '未提供预览'}</div>
                        {(candidate.path?.length ?? 0) > 0 && (
                          <div className="mt-1 text-[#8a6464]">路径：{candidate.path?.join(' / ')}</div>
                        )}
                        {(candidate.heading_scope?.length ?? 0) > 0 && (
                          <div className="mt-1 text-[#8a6464]">作用域：{candidate.heading_scope?.join(' / ')}</div>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </div>
        )}
      </div>
      <div ref={containerRef} className="flex-1 overflow-auto p-6">
        <div className="relative flex w-full flex-col items-center gap-8">
          {canRenderPreview ? (
            <div className="max-w-full" style={{ width: `${continuousScaledPaperWidthPx}px` }}>
            <div
              className="box-border rounded-[10px] border border-[#d9cab7] bg-white shadow-[0_28px_60px_rgba(76,56,37,0.18)]"
              style={{
                width: `${continuousBasePaperWidthPx}px`,
                transform: `scale(${continuousPaperScale})`,
                transformOrigin: 'top left',
              }}
            >
              {continuousHeaderPart && (
                <div className="px-10 pt-8 text-[#7a6652]">
                  {renderHeaderFooterSection(continuousHeaderPart, 'header', continuousPageContext)}
                </div>
              )}

              <div className="space-y-1.5 px-10 py-8">
                {previewItems.map((previewItem) => {
                  const paragraphId = previewItem.kind === 'block' && previewItem.block.type === 'paragraph'
                    ? previewItem.block.paragraph?.id
                    : undefined;
                  const anchoredEntries = paragraphId ? (orphanDeletedByAnchor.anchored.get(paragraphId) ?? []) : [];
                  return (
                    <React.Fragment key={previewItem.key}>
                      {anchoredEntries.map((entry) => renderDeletedInlineEntry(entry, `${previewItem.key}-deleted-${entry.id}`))}
                      {previewItem.kind === 'table' && previewItem.table
                        ? renderTable(previewItem.table, previewItem.key, continuousPageContext, false)
                        : renderBlock(previewItem.block, previewItem.key, continuousPageContext, false)}
                    </React.Fragment>
                  );
                })}
                {orphanDeletedByAnchor.trailing.map((entry) => renderDeletedInlineEntry(entry, `deleted-trailing-${entry.id}`))}
              </div>

              {continuousFooterPart && (
                <div className="px-10 pb-8 text-[#7a6652]">
                  {renderHeaderFooterSection(continuousFooterPart, 'footer', continuousPageContext)}
                </div>
              )}
            </div>
            </div>
          ) : (
            <div className="flex min-h-[320px] w-full max-w-[960px] items-center justify-center rounded-[10px] border border-[#d9cab7] bg-white text-sm text-[#7f6a57] shadow-[0_28px_60px_rgba(76,56,37,0.18)]">
              {previewPlaceholderText}
            </div>
          )}
        </div>
      </div>
    </div>
  );
});

WordPreview.displayName = 'WordPreview';
