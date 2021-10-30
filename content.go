package hub

import (
    "io"
    "net/http"
    "time"
)

var (
    contentClient = &http.Client{
        Timeout: 30 * time.Second,
    }
)

func HttpContent(topic string) ([]byte, string, error) {
    req, err := http.NewRequest(http.MethodGet, topic, nil)

    if err != nil {
        return nil, "", err
    }

    res, err := contentClient.Do(req)

    if err != nil {
        return nil, "", err
    }

    defer res.Body.Close()

    data, err := io.ReadAll(res.Body)

    if err != nil {
        return nil, "", err
    }

    contentType := res.Header.Get("Content-Type")

    if contentType == "" {
        contentType = "text/xml"
    }

    return data, contentType, nil
}