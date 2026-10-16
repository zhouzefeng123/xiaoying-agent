import { memo, useRef, useEffect, useState, useLayoutEffect, useCallback } from 'react';
import { MessageBubble, type Message, type ToolExecution } from './MessageBubble';
import { cn } from '../../lib/utils';
import { handleExternalLinkClick, isExternalLinkHref } from '../../lib/externalLinks';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

interface ChatAreaProps {
  messages: Message[];
  className?: string;
  isGenerating?: boolean;
  onCancelTool?: (executionId: string) => void;
  onResumeUser?: () => void;
  realtimeToolExecutions?: Map<string, ToolExecution[]>;
  fixedErrorMessage?: string | null;
}

interface HistoryMessageListProps {
  messages: Message[];
  onCancelTool?: (executionId: string) => void;
  onResumeUser?: () => void;
  realtimeToolExecutions?: Map<string, ToolExecution[]>;
}

interface LiveMessageSlotProps {
  message: Message | null;
  isGenerating?: boolean;
  onCancelTool?: (executionId: string) => void;
  onResumeUser?: () => void;
  realtimeToolExecutions?: ToolExecution[];
}

const BOTTOM_TOLERANCE_PX = 24;
const USER_SCROLL_COOLDOWN_MS = 160;

const HistoryMessageList = memo(({ messages, onCancelTool, onResumeUser, realtimeToolExecutions }: HistoryMessageListProps) => {
  const historyMessages = messages.slice(0, -1);

  return (
    <>
      {historyMessages.map((message) => (
        <div key={message.id} className="mb-6">
          <MessageBubble
            message={message}
            isThinking={false}
            onCancelTool={onCancelTool}
            realtimeToolExecutions={realtimeToolExecutions?.get(message.id)}
            onResume={onResumeUser}
          />
        </div>
      ))}
    </>
  );
}, (prev, next) => {
  if (prev.onCancelTool !== next.onCancelTool || prev.onResumeUser !== next.onResumeUser) {
    return false;
  }

  const prevHistoryLength = Math.max(0, prev.messages.length - 1);
  const nextHistoryLength = Math.max(0, next.messages.length - 1);
  if (prevHistoryLength !== nextHistoryLength) {
    return false;
  }

  for (let index = 0; index < nextHistoryLength; index += 1) {
    if (prev.messages[index] !== next.messages[index]) {
      return false;
    }

    const messageId = next.messages[index]?.id;
    if (!messageId) {
      continue;
    }

    if (prev.realtimeToolExecutions?.get(messageId) !== next.realtimeToolExecutions?.get(messageId)) {
      return false;
    }
  }

  return true;
});

const LiveMessageSlot = memo(({ message, isGenerating, onCancelTool, onResumeUser, realtimeToolExecutions }: LiveMessageSlotProps) => {
  if (!message) {
    return null;
  }

  const isThinking = Boolean(isGenerating && message.role === 'assistant');

  return (
    <div className="mb-6">
      <MessageBubble
        message={message}
        isThinking={isThinking}
        onCancelTool={onCancelTool}
        realtimeToolExecutions={realtimeToolExecutions}
        onResume={onResumeUser}
      />
    </div>
  );
}, (prev, next) => {
  return prev.message === next.message
    && prev.isGenerating === next.isGenerating
    && prev.onCancelTool === next.onCancelTool
    && prev.onResumeUser === next.onResumeUser
    && prev.realtimeToolExecutions === next.realtimeToolExecutions;
});

const ChatAreaComponent = ({ messages, className, isGenerating, onCancelTool, onResumeUser, realtimeToolExecutions, fixedErrorMessage }: ChatAreaProps) => {
  const scrollRef = useRef<HTMLDivElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const bottomAnchorRef = useRef<HTMLDivElement>(null);
  const lastObservedContentHeightRef = useRef(0);
  const userScrollUntilRef = useRef(0);
  const programmaticScrollUntilRef = useRef(0);
  const autoScrollRetryTimeoutRef = useRef<number | null>(null);

  const [shouldAutoScroll, setShouldAutoScroll] = useState(true);
  const shouldAutoScrollRef = useRef(true);

  const updateAutoScroll = useCallback((nextValue: boolean) => {
    shouldAutoScrollRef.current = nextValue;
    setShouldAutoScroll((prev) => (prev === nextValue ? prev : nextValue));
  }, []);

  const clearAutoScrollRetry = useCallback(() => {
    if (autoScrollRetryTimeoutRef.current === null) {
      return;
    }

    window.clearTimeout(autoScrollRetryTimeoutRef.current);
    autoScrollRetryTimeoutRef.current = null;
  }, []);

  const isAtBottom = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return true;
    const distance = el.scrollHeight - el.clientHeight - el.scrollTop;
    return distance <= BOTTOM_TOLERANCE_PX;
  }, []);

  const markUserScrolling = useCallback((durationMs: number = USER_SCROLL_COOLDOWN_MS) => {
    userScrollUntilRef.current = performance.now() + durationMs;
  }, []);

  const isUserScrolling = useCallback(() => {
    return performance.now() < userScrollUntilRef.current;
  }, []);

  const scrollToBottom = useCallback((behavior: ScrollBehavior = 'auto') => {
    programmaticScrollUntilRef.current = performance.now() + USER_SCROLL_COOLDOWN_MS;

    if (bottomAnchorRef.current) {
      bottomAnchorRef.current.scrollIntoView({ block: 'end', behavior });
      return;
    }

    const el = scrollRef.current;
    if (!el) return;

    const nextTop = Math.max(0, el.scrollHeight - el.clientHeight);
    if (behavior === 'smooth') {
      el.scrollTo({ top: nextTop, behavior });
    } else {
      el.scrollTop = nextTop;
    }
  }, []);

  const requestAutoScroll = useCallback((behavior: ScrollBehavior = 'auto') => {
    if (!shouldAutoScrollRef.current) {
      clearAutoScrollRetry();
      return;
    }

    if (!isUserScrolling()) {
      clearAutoScrollRetry();
      scrollToBottom(behavior);
      return;
    }

    const retryDelay = Math.max(0, userScrollUntilRef.current - performance.now()) + 8;
    clearAutoScrollRetry();
    autoScrollRetryTimeoutRef.current = window.setTimeout(() => {
      autoScrollRetryTimeoutRef.current = null;
      if (shouldAutoScrollRef.current && !isUserScrolling()) {
        scrollToBottom(behavior);
      }
    }, retryDelay);
  }, [clearAutoScrollRetry, isUserScrolling, scrollToBottom]);

  const lastMessage = messages[messages.length - 1] ?? null;
  const lastMessageRealtimeToolExecutions = lastMessage ? realtimeToolExecutions?.get(lastMessage.id) : undefined;
  const lastMessageProgressKey = lastMessage?.progress
    ? `${lastMessage.progress.percentage}:${lastMessage.progress.details}`
    : '';

  useEffect(() => {
    const root = scrollRef.current;
    const target = bottomAnchorRef.current;
    if (!root || !target) {
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
	      clearAutoScrollRetry();
          updateAutoScroll(true);
        }
      },
      {
        root,
        threshold: 0,
        rootMargin: `0px 0px ${BOTTOM_TOLERANCE_PX}px 0px`,
      }
    );

    observer.observe(target);
    return () => {
      observer.disconnect();
    };
  }, [clearAutoScrollRetry, messages.length, updateAutoScroll]);

  useEffect(() => clearAutoScrollRetry, [clearAutoScrollRetry]);

  const handleScroll = useCallback(() => {
    if (performance.now() <= programmaticScrollUntilRef.current) {
      return;
    }

    if (isAtBottom()) {
	  clearAutoScrollRetry();
	  updateAutoScroll(true);
      return;
    }

    clearAutoScrollRetry();
    updateAutoScroll(false);
  }, [clearAutoScrollRetry, isAtBottom, isUserScrolling, updateAutoScroll]);

  const handleWheel = useCallback(() => {
    markUserScrolling();
  }, [markUserScrolling]);

  const handleTouchStart = useCallback(() => {
    markUserScrolling(USER_SCROLL_COOLDOWN_MS * 2);
  }, [markUserScrolling]);

  const handleTouchMove = useCallback(() => {
    markUserScrolling(USER_SCROLL_COOLDOWN_MS * 2);
  }, [markUserScrolling]);

  useLayoutEffect(() => {
    if (messages.length > 0 && shouldAutoScrollRef.current) {
      requestAutoScroll('auto');
    }
  }, [messages.length, requestAutoScroll]);

  useLayoutEffect(() => {
    if (!lastMessage) return;
    if (shouldAutoScrollRef.current) {
      requestAutoScroll('auto');
    }
  }, [lastMessage, lastMessageProgressKey, requestAutoScroll]);

  useLayoutEffect(() => {
    if (!lastMessageRealtimeToolExecutions?.length) return;
    if (shouldAutoScrollRef.current) {
      requestAutoScroll('auto');
    }
  }, [lastMessageRealtimeToolExecutions, requestAutoScroll]);

  useLayoutEffect(() => {
    const content = contentRef.current;
    if (!content) return;

    lastObservedContentHeightRef.current = content.getBoundingClientRect().height;

    const observer = new ResizeObserver((entries) => {
      const nextHeight = entries[0]?.contentRect.height ?? content.getBoundingClientRect().height;
      if (Math.abs(nextHeight - lastObservedContentHeightRef.current) <= 0.5) {
        return;
      }

      lastObservedContentHeightRef.current = nextHeight;

      if (shouldAutoScrollRef.current) {
        requestAutoScroll('auto');
      }
    });

    observer.observe(content);
    return () => {
      observer.disconnect();
    };
  }, [isGenerating, messages.length, requestAutoScroll]);

  return (
    <div className={cn('flex-1 min-h-0 relative', className)}>
      {fixedErrorMessage ? (
        <div className="absolute left-4 right-4 top-3 z-20 pointer-events-none">
          <div className="pointer-events-auto rounded-xl border border-red-200 bg-red-50/95 px-4 py-3 text-sm text-red-700 dark:border-red-900/70 dark:bg-red-950/90 dark:text-red-200">
            <div className="mb-1 text-xs font-semibold tracking-wide text-red-500 dark:text-red-300">错误提示</div>
            <div className="prose prose-sm max-w-none break-words text-inherit prose-p:my-1 prose-a:text-red-600 hover:prose-a:text-red-700 dark:prose-a:text-red-300 dark:hover:prose-a:text-red-200">
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                components={{
                  a: ({ href, children, ...props }) => (
                    <a
                      {...props}
                      href={href}
                      className="font-medium underline decoration-2 underline-offset-2 cursor-pointer"
                      target={isExternalLinkHref(href) ? '_blank' : props.target}
                      rel={isExternalLinkHref(href) ? 'noopener noreferrer' : props.rel}
                      onClick={(event) => {
                        props.onClick?.(event);
                        if (!event.defaultPrevented) {
                          handleExternalLinkClick(event, href);
                        }
                      }}
                    >
                      {children}
                    </a>
                  ),
                }}
              >
                {fixedErrorMessage}
              </ReactMarkdown>
            </div>
          </div>
        </div>
      ) : null}
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        onWheel={handleWheel}
        onTouchStart={handleTouchStart}
        onTouchMove={handleTouchMove}
        className={cn(
          'h-full w-full overflow-y-auto scrollbar-thin scrollbar-thumb-zinc-200 dark:scrollbar-thumb-zinc-800 scrollbar-track-transparent px-4',
          fixedErrorMessage ? 'pt-24' : ''
        )}
      >
        {messages.length === 0 ? (
          <div className="h-full flex flex-col items-center justify-center text-center text-gray-500">
            <p></p>
          </div>
        ) : (
          <div ref={contentRef} className="chat-content-rail w-full py-2">
            <HistoryMessageList
              messages={messages}
              onCancelTool={onCancelTool}
              onResumeUser={onResumeUser}
              realtimeToolExecutions={realtimeToolExecutions}
            />
            <LiveMessageSlot
              message={lastMessage}
              isGenerating={isGenerating}
              onCancelTool={onCancelTool}
              onResumeUser={onResumeUser}
              realtimeToolExecutions={lastMessageRealtimeToolExecutions}
            />
            <div ref={bottomAnchorRef} className="h-px w-full" aria-hidden="true" />
          </div>
        )}
      </div>
    </div>
  );
};

export const ChatArea = memo(ChatAreaComponent);
