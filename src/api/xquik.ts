import type { XPostEntity } from '../types/x.ts';

export const XQUIK_SEARCH_URL = 'https://xquik.com/api/v1/x/tweets/search';

export interface XquikSearchUrlOptions {
  q: string;
  limit?: number;
  queryType?: 'Latest' | 'Top';
  cursor?: string;
  baseUrl?: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function firstValue(...values: unknown[]): unknown {
  return values.find((value) => value !== undefined && value !== null);
}

function asString(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

function asNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === 'string') {
    const parsed = Number(value.replaceAll(',', ''));
    return Number.isFinite(parsed) ? parsed : undefined;
  }
  return undefined;
}

function tweetArray(payload: unknown): Record<string, unknown>[] {
  if (!isRecord(payload)) {
    return [];
  }
  for (const key of ['tweets', 'results', 'data']) {
    const value = payload[key];
    if (Array.isArray(value)) {
      return value.filter(isRecord);
    }
  }
  const tweet = payload['tweet'];
  return isRecord(tweet) ? [tweet] : [payload];
}

export function buildXquikSearchUrl({
  q,
  limit = 20,
  queryType = 'Latest',
  cursor,
  baseUrl = XQUIK_SEARCH_URL,
}: XquikSearchUrlOptions): string {
  const query = q.trim();
  if (!query) {
    throw new Error('Xquik search query is required');
  }
  const url = new URL(baseUrl);
  url.searchParams.set('q', query);
  url.searchParams.set('limit', String(limit));
  url.searchParams.set('queryType', queryType);
  if (cursor) {
    url.searchParams.set('cursor', cursor);
  }
  return url.toString();
}

export function xquikTweetToPost(raw: Record<string, unknown>): XPostEntity {
  const author = isRecord(raw['author']) ? raw['author'] : {};
  const id = asString(firstValue(raw['id'], raw['tweet_id'], raw['rest_id']));
  if (!id) {
    throw new Error('Xquik tweet id is required');
  }

  return {
    id,
    author_id: asString(firstValue(author['id'], raw['author_id'])),
    text: asString(firstValue(raw['text'], raw['full_text'], raw['content'])),
    created_at: asString(firstValue(raw['created_at'], raw['createdAt'])),
    conversation_id: asString(firstValue(raw['conversation_id'], raw['conversationId'])),
    lang: asString(raw['lang']),
    possibly_sensitive: Boolean(raw['possibly_sensitive']),
    public_metrics: {
      like_count: asNumber(firstValue(raw['likeCount'], raw['like_count'], raw['likes'])) ?? 0,
      retweet_count:
        asNumber(firstValue(raw['retweetCount'], raw['retweet_count'], raw['retweets'])) ??
          0,
      reply_count: asNumber(firstValue(raw['replyCount'], raw['reply_count'], raw['replies'])) ?? 0,
      quote_count: asNumber(firstValue(raw['quoteCount'], raw['quote_count'], raw['quotes'])) ?? 0,
    },
    raw_xquik_author: author,
  };
}

export function postsFromXquikPayload(payload: unknown): XPostEntity[] {
  return tweetArray(payload).map(xquikTweetToPost);
}
