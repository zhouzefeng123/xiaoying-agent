import type {
  GoWordDocumentSection,
  GoWordHeaderFooterPart,
  GoWordParagraph,
  GoWordSectionLayoutPatch,
} from './types';

export type ResolvedPageLayout = {
  pageWidth: number;
  pageHeight: number;
  orientation: string;
  titlePage: boolean;
  titlePageSet: boolean;
  pageNumberStart?: number;
  pageNumberFormat?: string;
  marginTop: number;
  marginRight: number;
  marginBottom: number;
  marginLeft: number;
  marginHeader: number;
  marginFooter: number;
  marginGutter: number;
};

export type PageNumberContext = {
  globalPageNumber: number;
  sectionPageOrdinal: number;
  displayedPageNumber: number;
  totalPages: number;
};

export function twipsToPx(value?: number): number | undefined {
  if (!value) {
    return undefined;
  }
  return Math.round(value / 15);
}

export function resolvePageLayout(layout?: GoWordSectionLayoutPatch | null): ResolvedPageLayout {
  const pageWidth = layout?.page_width ?? 11906;
  const pageHeight = layout?.page_height ?? 16838;
  return {
    pageWidth,
    pageHeight,
    orientation: layout?.orientation || (pageWidth > pageHeight ? 'landscape' : 'portrait'),
    titlePage: Boolean(layout?.title_page),
    titlePageSet: Boolean(layout?.title_page_set),
    pageNumberStart: layout?.page_number_start,
    pageNumberFormat: layout?.page_number_format,
    marginTop: layout?.margin_top ?? 1440,
    marginRight: layout?.margin_right ?? 1800,
    marginBottom: layout?.margin_bottom ?? 1440,
    marginLeft: layout?.margin_left ?? 1800,
    marginHeader: layout?.margin_header ?? 720,
    marginFooter: layout?.margin_footer ?? 720,
    marginGutter: layout?.margin_gutter ?? 0,
  };
}

export function normalizeSectionBreakType(value?: string): string {
  return value?.trim().toLowerCase() ?? '';
}

export function pageContentWidthFromLayout(layout: ResolvedPageLayout): number {
  const pageWidthPx = twipsToPx(layout.pageWidth) ?? 794;
  const marginLeftPx = twipsToPx(layout.marginLeft) ?? 120;
  const marginRightPx = twipsToPx(layout.marginRight) ?? 120;
  return Math.max(pageWidthPx - marginLeftPx - marginRightPx, 320);
}

export function pageContentHeightFromLayout(layout: ResolvedPageLayout): number {
  const pageHeightPx = twipsToPx(layout.pageHeight) ?? 1122;
  const marginTopPx = twipsToPx(layout.marginTop) ?? 96;
  const marginBottomPx = twipsToPx(layout.marginBottom) ?? 96;
  return Math.max(pageHeightPx - marginTopPx - marginBottomPx, 320);
}

export function sectionKey(section: GoWordDocumentSection | null | undefined): string {
  if (!section) {
    return 'default';
  }
  return `${section.start_block_index ?? 0}:${section.end_block_index ?? Number.MAX_SAFE_INTEGER}`;
}

export function sectionLayoutsSharePageGeometry(
  left?: GoWordSectionLayoutPatch | null,
  right?: GoWordSectionLayoutPatch | null,
): boolean {
  const leftLayout = resolvePageLayout(left);
  const rightLayout = resolvePageLayout(right);
  return leftLayout.pageWidth === rightLayout.pageWidth
    && leftLayout.pageHeight === rightLayout.pageHeight
    && leftLayout.orientation === rightLayout.orientation
    && leftLayout.marginTop === rightLayout.marginTop
    && leftLayout.marginRight === rightLayout.marginRight
    && leftLayout.marginBottom === rightLayout.marginBottom
    && leftLayout.marginLeft === rightLayout.marginLeft
    && leftLayout.marginHeader === rightLayout.marginHeader
    && leftLayout.marginFooter === rightLayout.marginFooter
    && leftLayout.marginGutter === rightLayout.marginGutter;
}

export function sectionBreakForcesPageBreak(paragraph: GoWordParagraph, currentSection?: GoWordDocumentSection | null): boolean {
  if (!paragraph.section_break_layout) {
    return false;
  }
  const breakType = normalizeSectionBreakType(paragraph.section_break_layout.break_type);
  if (breakType === 'continuous') {
    return !sectionLayoutsSharePageGeometry(paragraph.section_break_layout, currentSection?.layout);
  }
  return true;
}

export function sectionStartNeedsBlankPage(layout: GoWordSectionLayoutPatch | null | undefined, nextPageNumber: number): boolean {
  const breakType = normalizeSectionBreakType(layout?.break_type);
  if (breakType === 'oddpage') {
    return nextPageNumber % 2 === 0;
  }
  if (breakType === 'evenpage') {
    return nextPageNumber % 2 === 1;
  }
  return false;
}

function toRoman(value: number): string {
  if (value <= 0) {
    return String(value);
  }
  const numerals: Array<[number, string]> = [
    [1000, 'M'], [900, 'CM'], [500, 'D'], [400, 'CD'],
    [100, 'C'], [90, 'XC'], [50, 'L'], [40, 'XL'],
    [10, 'X'], [9, 'IX'], [5, 'V'], [4, 'IV'], [1, 'I'],
  ];
  let remaining = value;
  let result = '';
  for (const [digit, symbol] of numerals) {
    while (remaining >= digit) {
      remaining -= digit;
      result += symbol;
    }
  }
  return result;
}

function toAlphabetic(value: number): string {
  if (value <= 0) {
    return String(value);
  }
  let remaining = value;
  let result = '';
  while (remaining > 0) {
    remaining -= 1;
    result = String.fromCharCode(65 + (remaining % 26)) + result;
    remaining = Math.floor(remaining / 26);
  }
  return result;
}

export function formatPageNumber(value: number, format?: string): string {
  const normalized = format?.trim().toLowerCase();
  switch (normalized) {
    case 'upperroman':
      return toRoman(value);
    case 'lowerroman':
      return toRoman(value).toLowerCase();
    case 'upperletter':
      return toAlphabetic(value);
    case 'lowerletter':
      return toAlphabetic(value).toLowerCase();
    case 'decimalzero':
      return value < 10 ? `0${value}` : String(value);
    default:
      return String(value);
  }
}

export function applyPageNumberText(text: string, pageNumber: number, totalPages: number, format?: string): string {
  const formattedPageNumber = formatPageNumber(pageNumber, format);
  const formattedTotalPages = formatPageNumber(totalPages, format);
  let nextText = text;
  if (/第\s*[^\s]+\s*页/g.test(nextText)) {
    nextText = nextText.replace(/第\s*[^\s]+\s*页/g, `第 ${formattedPageNumber} 页`);
  }
  if (/共\s*[^\s]+\s*页/g.test(nextText)) {
    nextText = nextText.replace(/共\s*[^\s]+\s*页/g, `共 ${formattedTotalPages} 页`);
  }
  nextText = nextText.replace(/\{\{PAGE\}\}/g, formattedPageNumber);
  nextText = nextText.replace(/\{\{TOTAL_PAGES\}\}/g, formattedTotalPages);
  return nextText;
}

export function pickHeaderFooterPart(
  parts: GoWordHeaderFooterPart[],
  kind: 'header' | 'footer',
  pageContext: Pick<PageNumberContext, 'sectionPageOrdinal' | 'displayedPageNumber'>,
  layout?: GoWordSectionLayoutPatch | null,
): GoWordHeaderFooterPart | null {
  const candidates = parts.filter((part) => part.kind === kind);
  if (candidates.length === 0) {
    return null;
  }
  const first = candidates.find((part) => (part.variant ?? 'default') === 'first');
  const even = candidates.find((part) => (part.variant ?? 'default') === 'even');
  const defaultPart = candidates.find((part) => (part.variant ?? 'default') === 'default') ?? candidates[0] ?? null;
  const resolvedLayout = resolvePageLayout(layout);
  const allowFirstPage = resolvedLayout.titlePage || (!resolvedLayout.titlePageSet && Boolean(first));
  if (pageContext.sectionPageOrdinal === 1 && allowFirstPage && first) {
    return first;
  }
  if (pageContext.displayedPageNumber % 2 === 0 && even) {
    return even;
  }
  return defaultPart;
}

export function buildPageNumberContexts(sections: Array<GoWordDocumentSection | null>): PageNumberContext[] {
  const results: PageNumberContext[] = [];
  let currentSectionKey = '';
  let currentDisplayedPageNumber = 0;
  let currentSectionPageOrdinal = 0;

  sections.forEach((section, index) => {
    const nextSectionKey = sectionKey(section);
    const isNewSection = index === 0 || nextSectionKey !== currentSectionKey;
    if (isNewSection) {
      currentSectionKey = nextSectionKey;
      currentSectionPageOrdinal = 1;
      const layout = resolvePageLayout(section?.layout);
      if (index === 0) {
        currentDisplayedPageNumber = layout.pageNumberStart ?? 1;
      } else {
        currentDisplayedPageNumber = layout.pageNumberStart ?? (currentDisplayedPageNumber + 1);
      }
    } else {
      currentSectionPageOrdinal += 1;
      currentDisplayedPageNumber += 1;
    }

    results.push({
      globalPageNumber: index + 1,
      sectionPageOrdinal: currentSectionPageOrdinal,
      displayedPageNumber: currentDisplayedPageNumber,
      totalPages: sections.length,
    });
  });

  return results;
}