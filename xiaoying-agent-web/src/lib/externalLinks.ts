import type { MouseEvent } from 'react';

const EXTERNAL_LINK_PATTERN = /^(https?:\/\/|mailto:)/i;

export function isExternalLinkHref(href?: string | null): href is string {
    return typeof href === 'string' && EXTERNAL_LINK_PATTERN.test(href.trim());
}

export function openLinkExternally(href: string): void {
    const normalizedHref = href.trim();
    if (!normalizedHref) {
        return;
    }

    if (window.electron?.openExternal && isExternalLinkHref(normalizedHref)) {
        window.electron.openExternal(normalizedHref);
        return;
    }

    window.open(normalizedHref, '_blank', 'noopener,noreferrer');
}

export function handleExternalLinkClick(event: MouseEvent<HTMLAnchorElement>, href?: string | null): void {
    if (!isExternalLinkHref(href)) {
        return;
    }

    event.preventDefault();
    openLinkExternally(href);
}