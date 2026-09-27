package jellyfin

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const maxImageResponseBytes = 4 << 20

// ItemImage returns one bounded Jellyfin image without exposing the API key.
func (c *Client) ItemImage(
	ctx context.Context, itemID string, imageType core.ItemImageType, maxWidth int, ifNoneMatch string,
) (core.ItemImage, error) {
	if !core.ValidAccountMediaUserID(itemID) || !imageType.Valid() ||
		maxWidth < core.MinItemImageWidth || maxWidth > core.MaxItemImageWidth {
		return core.ItemImage{}, core.ErrInvalidArgument
	}
	path := "/Items/" + url.PathEscape(itemID) + "/Images/" + string(imageType) +
		"?maxWidth=" + strconv.Itoa(maxWidth) + "&quality=90"
	image, started, err := c.getImageWithRetry(ctx, path, ifNoneMatch)
	if err == nil {
		c.observe("item_image", "success", started)
	}
	return image, err
}

func (c *Client) getImageWithRetry(
	ctx context.Context, path, ifNoneMatch string,
) (core.ItemImage, time.Time, error) {
	operationCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	var last error
	for attempt := range maxAttempts {
		started := c.now()
		image, retry, err := c.imageAttempt(operationCtx, path, ifNoneMatch, started)
		if err == nil {
			return image, started, nil
		}
		last = err
		if !retry || attempt == maxAttempts-1 {
			if retry {
				c.observeRetry("item_image", "exhausted")
			}
			return core.ItemImage{}, time.Time{}, last
		}
		if err := c.waitImageRetry(operationCtx, last, attempt); err != nil {
			return core.ItemImage{}, time.Time{}, err
		}
	}
	return core.ItemImage{}, time.Time{}, last
}

func (c *Client) waitImageRetry(ctx context.Context, last error, attempt int) error {
	delay := c.retryDelay(last, attempt)
	if !delayFits(ctx, c.now(), delay) {
		c.observeRetry("item_image", "budget_exhausted")
		return withRetryDelay(last, delay)
	}
	c.observeRetry("item_image", "scheduled")
	if err := c.sleep(ctx, delay); err != nil {
		return mediaError("item_image", core.MediaServerUnavailable, err)
	}
	return nil
}

func (c *Client) imageAttempt(
	ctx context.Context, path, ifNoneMatch string, started time.Time,
) (core.ItemImage, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return core.ItemImage{}, false, mediaError("item_image", core.MediaServerMalformed, err)
	}
	req.Header.Set("Authorization", c.authorization)
	req.Header.Set("Accept", "image/jpeg, image/png, image/webp")
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		_, retry, classified := c.classifyTransportError(ctx, "item_image", started, err)
		return core.ItemImage{}, retry, classified
	}
	defer func() {
		_, drainErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes+1))
		_ = drainErr
		closeErr := resp.Body.Close()
		_ = closeErr
	}()
	return c.readImageResponse(ctx, resp, started)
}

func (c *Client) readImageResponse(
	ctx context.Context, resp *http.Response, started time.Time,
) (core.ItemImage, bool, error) {
	if resp.StatusCode == http.StatusNotModified {
		return core.ItemImage{ETag: resp.Header.Get("ETag"), NotModified: true}, false, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, retry, err := c.classifyResponse("item_image", resp, nil, started)
		return core.ItemImage{}, retry, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageResponseBytes+1))
	if err != nil {
		_, retry, classified := c.classifyTransportError(ctx, "item_image", started, err)
		return core.ItemImage{}, retry, classified
	}
	if len(body) > maxImageResponseBytes {
		c.observe("item_image", "malformed", started)
		return core.ItemImage{}, false, mediaError("item_image", core.MediaServerMalformed, errors.New("response exceeds size limit"))
	}
	contentType, err := allowedImageContentType(resp.Header.Get("Content-Type"))
	if err != nil {
		c.observe("item_image", "malformed", started)
		return core.ItemImage{}, false, mediaError("item_image", core.MediaServerMalformed, err)
	}
	return core.ItemImage{Body: body, ContentType: contentType, ETag: resp.Header.Get("ETag")}, false, nil
}

func allowedImageContentType(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "", errors.New("invalid image content type")
	}
	switch mediaType {
	case "image/jpeg", "image/png", "image/webp":
		return mediaType, nil
	default:
		return "", errors.New("unsupported image content type")
	}
}
