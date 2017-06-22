package deleter

import (
	"fmt"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/coreos/grafiti/arn"
)

// S3ObjectDeleter represents a collection of AWS S3 objects
type S3ObjectDeleter struct {
	Client            s3iface.S3API
	ResourceType      arn.ResourceType
	BucketName        arn.ResourceName
	ObjectIdentifiers []*s3.ObjectIdentifier
}

func (rd *S3ObjectDeleter) String() string {
	return fmt.Sprintf(`{"Type": "%s", "BucketName": "%s", "ObjectIdentifiers": %v}`, rd.ResourceType, rd.BucketName, rd.ObjectIdentifiers)
}

// AddResourceNames adds S3 object names to ResourceNames
func (rd *S3ObjectDeleter) AddResourceNames(keys ...arn.ResourceName) {
	for _, key := range keys {
		rd.ObjectIdentifiers = append(rd.ObjectIdentifiers, &s3.ObjectIdentifier{Key: key.AWSString()})
	}
}

// DeleteResources deletes S3 objects from AWS
func (rd *S3ObjectDeleter) DeleteResources(cfg *DeleteConfig) error {
	if rd.BucketName == "" || len(rd.ObjectIdentifiers) == 0 {
		return nil
	}

	fmtStr := "Deleted S3 Object"
	if cfg.DryRun {
		for _, o := range rd.ObjectIdentifiers {
			fmt.Printf("%s %s %s from S3 Bucket %s\n", drStr, fmtStr, *o.Key, rd.BucketName)
		}
		return nil
	}

	params := &s3.DeleteObjectsInput{
		Bucket: rd.BucketName.AWSString(),
		Delete: &s3.Delete{Objects: rd.ObjectIdentifiers},
	}

	if rd.Client == nil {
		rd.Client = s3.New(setUpAWSSession())
	}

	ctx := aws.BackgroundContext()
	resp, err := rd.Client.DeleteObjectsWithContext(ctx, params)
	if err != nil {
		for _, o := range rd.ObjectIdentifiers {
			cfg.logDeleteError(arn.S3ObjectRType, arn.ResourceName(*o.Key), err, logrus.Fields{
				"parent_resource_type": arn.S3BucketRType,
				"parent_resource_name": *o.Key,
			})
		}
		if cfg.IgnoreErrors {
			return nil
		}
		return err
	}

	for _, o := range resp.Deleted {
		fmt.Printf("%s %s from S3 Bucket %s\n", fmtStr, *o.Key, rd.BucketName)
	}

	return nil
}

// RequestS3ObjectsByBucket requests S3 objects by bucket name from the AWS API
func (rd *S3ObjectDeleter) RequestS3ObjectsByBucket() ([]*s3.Object, error) {
	params := &s3.ListObjectsV2Input{
		Bucket: rd.BucketName.AWSString(),
	}
	objs := make([]*s3.Object, 0)

	if rd.Client == nil {
		rd.Client = s3.New(setUpAWSSession())
	}

	for {
		ctx := aws.BackgroundContext()
		resp, err := rd.Client.ListObjectsV2WithContext(ctx, params)
		if err != nil {
			return nil, err
		}

		for _, o := range resp.Contents {
			objs = append(objs, o)
		}

		if resp.IsTruncated == nil || !*resp.IsTruncated {
			break
		}

		params.ContinuationToken = resp.ContinuationToken
	}

	return objs, nil
}

// S3BucketDeleter represents a collection of AWS S3 buckets
type S3BucketDeleter struct {
	Client        s3iface.S3API
	ResourceType  arn.ResourceType
	ResourceNames arn.ResourceNames
}

func (rd *S3BucketDeleter) String() string {
	return fmt.Sprintf(`{"Type": "%s", "ResourceNames": %v}`, rd.ResourceType, rd.ResourceNames)
}

// AddResourceNames adds S3 bucket names to ResourceNames
func (rd *S3BucketDeleter) AddResourceNames(ns ...arn.ResourceName) {
	rd.ResourceNames = append(rd.ResourceNames, ns...)
}

// DeleteResources deletes S3 buckets from AWS
func (rd *S3BucketDeleter) DeleteResources(cfg *DeleteConfig) error {
	if len(rd.ResourceNames) == 0 {
		return nil
	}

	fmtStr := "Deleted S3 Bucket"
	if cfg.DryRun {
		for _, n := range rd.ResourceNames {
			fmt.Println(drStr, fmtStr, n)
		}
		return nil
	}

	if rd.Client == nil {
		rd.Client = s3.New(setUpAWSSession())
	}

	var (
		params *s3.DeleteBucketInput
		objDel *S3ObjectDeleter
	)
	for _, n := range rd.ResourceNames {
		// Delete all objects in bucket
		objDel = &S3ObjectDeleter{BucketName: n}
		objs, oerr := objDel.RequestS3ObjectsByBucket()
		if oerr != nil {
			continue
		}

		for _, obj := range objs {
			objDel.AddResourceNames(arn.ResourceName(*obj.Key))
		}
		if err := objDel.DeleteResources(cfg); err != nil && !cfg.IgnoreErrors {
			return err
		}

		params = &s3.DeleteBucketInput{
			Bucket: n.AWSString(),
		}

		// Prevent throttling
		time.Sleep(cfg.BackoffTime)

		ctx := aws.BackgroundContext()
		_, err := rd.Client.DeleteBucketWithContext(ctx, params)
		if err != nil {
			cfg.logDeleteError(arn.S3BucketRType, n, err)
			if cfg.IgnoreErrors {
				continue
			}
			return err
		}

		fmt.Println(fmtStr, n)
	}

	return nil
}
