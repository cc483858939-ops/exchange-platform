import { describe, expect, it } from 'vitest';
import { calculatePreviewDimensions } from './localImagePreview';

describe('calculatePreviewDimensions', () => {
  it('fits a landscape image within the maximum side without stretching it', () => {
    expect(calculatePreviewDimensions(4032, 3024)).toEqual({ width: 1024, height: 768 });
  });

  it('fits a portrait image within the maximum side without stretching it', () => {
    expect(calculatePreviewDimensions(3024, 4032)).toEqual({ width: 768, height: 1024 });
  });

  it('does not upscale an image that is already smaller than the maximum side', () => {
    expect(calculatePreviewDimensions(800, 600)).toEqual({ width: 800, height: 600 });
  });

  it('handles extreme aspect ratios while keeping every dimension positive', () => {
    expect(calculatePreviewDimensions(1, 8000)).toEqual({ width: 1, height: 1024 });
  });

  it('returns null for invalid or non-positive dimensions', () => {
    expect(calculatePreviewDimensions(0, 100)).toBeNull();
    expect(calculatePreviewDimensions(-1, 100)).toBeNull();
    expect(calculatePreviewDimensions(Number.NaN, 100)).toBeNull();
    expect(calculatePreviewDimensions(100, Number.POSITIVE_INFINITY)).toBeNull();
  });
});
