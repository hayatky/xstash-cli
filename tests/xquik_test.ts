import { assertEquals, assertThrows } from '@std/assert';
import { buildXquikSearchUrl, postsFromXquikPayload, xquikTweetToPost } from '../src/api/xquik.ts';

Deno.test('buildXquikSearchUrl builds the public search endpoint URL', () => {
  const url = buildXquikSearchUrl({
    q: 'from:alice',
    limit: 5,
    cursor: 'next',
  });

  assertEquals(url.includes('https://xquik.com/api/v1/x/tweets/search'), true);
  assertEquals(url.includes('q=from%3Aalice'), true);
  assertEquals(url.includes('limit=5'), true);
  assertEquals(url.includes('cursor=next'), true);
});

Deno.test('buildXquikSearchUrl rejects empty queries', () => {
  assertThrows(() => buildXquikSearchUrl({ q: ' ' }), Error, 'query is required');
});

Deno.test('xquikTweetToPost preserves zero metrics', () => {
  const post = xquikTweetToPost({
    id: '123',
    text: 'hello',
    likeCount: 0,
    replyCount: '2',
    author: { id: 'u1', username: 'alice' },
  });

  assertEquals(post.id, '123');
  assertEquals(post.author_id, 'u1');
  assertEquals(post.text, 'hello');
  assertEquals(post.public_metrics?.like_count, 0);
  assertEquals(post.public_metrics?.reply_count, 2);
});

Deno.test('postsFromXquikPayload reads search result arrays', () => {
  const posts = postsFromXquikPayload({
    tweets: [
      { id: '1', text: 'one' },
      { id: '2', text: 'two' },
    ],
  });

  assertEquals(posts.map((post) => post.id), ['1', '2']);
});
